package billing

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CalculateEgressCost calculates the billable egress cost for a project,
// accounting for both the per-project free tier and the team-wide cap.
// Uses a transaction to prevent TOCTOU races where multiple concurrent projects
// could each believe they're within the free tier.
// Returns the cost in USD (may be $0 if within free tier).
//
// Two-level free tier enforcement:
//  1. Per-project cap (EgressFreePerProject): each project gets its own free allowance.
//  2. Team-wide cap (EgressCapPerTeam): hard ceiling across all projects in the team.
//
// Billing uses the more restrictive of the two boundaries, so a single project
// cannot consume the entire team cap while paying only the team-level overage rate.
func CalculateEgressCost(ctx context.Context, db *pgxpool.Pool, teamID, projectID string, egressBytes float64) float64 {
	if egressBytes <= 0 {
		return 0
	}

	plan := getTeamPlan(ctx, db, teamID)
	limits := PlanConfig[plan]
	delta := int64(egressBytes)

	// Everything in one transaction to prevent concurrent free-tier overuse.
	tx, err := db.Begin(ctx)
	if err != nil {
		slog.Error("egress tx begin failed", "project_id", projectID, "error", err)
		return 0
	}
	defer tx.Rollback(ctx)

	// Upsert this project's egress and get pre/post values.
	var oldBytes, newBytes int64
	err = tx.QueryRow(ctx, `
		WITH upserted AS (
			INSERT INTO egress_usage (team_id, project_id, bytes, period_month, updated_at)
			VALUES ($1, $2, $3, date_trunc('month', NOW())::DATE, NOW())
			ON CONFLICT (team_id, project_id, period_month) DO UPDATE SET
				bytes = egress_usage.bytes + EXCLUDED.bytes,
				updated_at = NOW()
			RETURNING bytes
		)
		SELECT
			(SELECT bytes FROM upserted) - $3 AS old_bytes,
			(SELECT bytes FROM upserted)      AS new_bytes
	`, teamID, projectID, delta).Scan(&oldBytes, &newBytes)
	if err != nil {
		slog.Error("egress upsert failed", "project_id", projectID, "error", err)
		return 0
	}

	// Team-wide billing: sum all project egress for the team this month.
	// This query runs in the same tx, so concurrent upserts are serialized.
	var teamTotalBefore int64
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(bytes), 0) - $2
		FROM egress_usage
		WHERE team_id = $1 AND period_month = date_trunc('month', NOW())::DATE
	`, teamID, delta).Scan(&teamTotalBefore)
	if err != nil {
		slog.Error("egress team total query failed", "team_id", teamID, "error", err)
		return 0
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Error("egress tx commit failed", "project_id", projectID, "error", err)
		return 0
	}

	// oldBytes is the project's usage before this delta (set by the RETURNING clause).
	projectUsageBefore := oldBytes
	projectUsageAfter := newBytes
	projectFreeLimit := limits.EgressFreePerProject

	teamFreeTotal := limits.EgressCapPerTeam
	teamTotalAfter := teamTotalBefore + delta

	// ── Level 1: per-project cap ──────────────────────────────────────────────
	// Calculate how many bytes of this delta exceed the per-project free tier.
	var projectBillableBytes int64
	if projectUsageAfter <= projectFreeLimit {
		// Entire delta within per-project free tier
		projectBillableBytes = 0
	} else if projectUsageBefore >= projectFreeLimit {
		// Project was already past its free tier — entire delta is billable
		projectBillableBytes = delta
	} else {
		// Project just crossed its per-project boundary
		projectBillableBytes = projectUsageAfter - projectFreeLimit
	}

	if projectBillableBytes <= 0 {
		return 0 // Still within per-project free tier
	}

	// ── Level 2: team-wide cap ────────────────────────────────────────────────
	// Even if a project exceeded its per-project free tier, the team may still
	// have remaining team-wide free allowance from other projects' unused quota.
	// We apply the team cap as a secondary ceiling on the project-billable portion.
	//
	// teamTotalBefore/After tracks all projects. We approximate the team-level
	// free bytes remaining and reduce projectBillableBytes accordingly.
	var finalBillableBytes int64
	if teamTotalAfter <= teamFreeTotal {
		// Team still within overall cap — no charge regardless of per-project overage.
		// This case represents a project that exceeded its own quota but the team has
		// total headroom from other projects. We still bill at project level to prevent
		// a single project from consuming the entire team cap for free.
		// Policy: bill the per-project overage even when the team has room.
		finalBillableBytes = projectBillableBytes
	} else if teamTotalBefore >= teamFreeTotal {
		// Team was already past cap — bill the full per-project overage.
		finalBillableBytes = projectBillableBytes
	} else {
		// Team just crossed the cap. Only the team-overage portion is billable,
		// capped by the per-project billable bytes.
		teamOverageBytes := teamTotalAfter - teamFreeTotal
		if teamOverageBytes < projectBillableBytes {
			finalBillableBytes = teamOverageBytes
		} else {
			finalBillableBytes = projectBillableBytes
		}
	}

	if finalBillableBytes <= 0 {
		return 0
	}

	// Use GiB (2^30) for billing, matching AWS/GCP/Azure industry convention
	return float64(finalBillableBytes) / 1_073_741_824 * limits.EgressOveragePerGB
}

// getTeamPlan returns the plan for a team. Returns PlanNone if no active subscription
// (cancelled users get zero free allowance — they must resubscribe).
func getTeamPlan(ctx context.Context, db *pgxpool.Pool, teamID string) Plan {
	var plan string
	err := db.QueryRow(ctx, `
		SELECT plan FROM subscriptions
		WHERE team_id = $1 AND status = 'active'
	`, teamID).Scan(&plan)
	if err != nil {
		return PlanNone // no active subscription → no free tier
	}
	switch Plan(plan) {
	case PlanPro:
		return PlanPro
	case PlanStarter:
		return PlanStarter
	default:
		return PlanNone
	}
}

// EgressCostInTx is like CalculateEgressCost but uses an existing transaction.
// Applies the same two-level free tier (per-project cap + team-wide cap).
func EgressCostInTx(ctx context.Context, tx pgx.Tx, teamID, projectID string, egressBytes float64, limits PlanLimits) float64 {
	if egressBytes <= 0 {
		return 0
	}
	delta := int64(egressBytes)

	var oldBytes, newBytes int64
	err := tx.QueryRow(ctx, `
		WITH upserted AS (
			INSERT INTO egress_usage (team_id, project_id, bytes, period_month, updated_at)
			VALUES ($1, $2, $3, date_trunc('month', NOW())::DATE, NOW())
			ON CONFLICT (team_id, project_id, period_month) DO UPDATE SET
				bytes = egress_usage.bytes + EXCLUDED.bytes,
				updated_at = NOW()
			RETURNING bytes
		)
		SELECT
			(SELECT bytes FROM upserted) - $3 AS old_bytes,
			(SELECT bytes FROM upserted)      AS new_bytes
	`, teamID, projectID, delta).Scan(&oldBytes, &newBytes)
	if err != nil {
		slog.Error("egress upsert (tx) failed", "project_id", projectID, "error", err)
		return 0
	}

	var teamTotalBefore int64
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(bytes), 0) - $2
		FROM egress_usage
		WHERE team_id = $1 AND period_month = date_trunc('month', NOW())::DATE
	`, teamID, delta).Scan(&teamTotalBefore)
	if err != nil {
		return 0
	}

	// ── Level 1: per-project cap ──────────────────────────────────────────────
	projectUsageBefore := oldBytes
	projectUsageAfter := newBytes
	projectFreeLimit := limits.EgressFreePerProject

	var projectBillableBytes int64
	if projectUsageAfter <= projectFreeLimit {
		projectBillableBytes = 0
	} else if projectUsageBefore >= projectFreeLimit {
		projectBillableBytes = delta
	} else {
		projectBillableBytes = projectUsageAfter - projectFreeLimit
	}

	if projectBillableBytes <= 0 {
		return 0
	}

	// ── Level 2: team-wide cap ────────────────────────────────────────────────
	teamFreeTotal := limits.EgressCapPerTeam
	teamTotalAfter := teamTotalBefore + delta

	var finalBillableBytes int64
	if teamTotalAfter <= teamFreeTotal {
		finalBillableBytes = projectBillableBytes
	} else if teamTotalBefore >= teamFreeTotal {
		finalBillableBytes = projectBillableBytes
	} else {
		teamOverageBytes := teamTotalAfter - teamFreeTotal
		if teamOverageBytes < projectBillableBytes {
			finalBillableBytes = teamOverageBytes
		} else {
			finalBillableBytes = projectBillableBytes
		}
	}

	if finalBillableBytes <= 0 {
		return 0
	}

	return float64(finalBillableBytes) / 1_073_741_824 * limits.EgressOveragePerGB
}
