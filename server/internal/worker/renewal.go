package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// DomainRenewalJobArgs is the payload for the daily domain renewal check.
type DomainRenewalJobArgs struct{}

func (DomainRenewalJobArgs) Kind() string { return "domain_renewal" }

func (DomainRenewalJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       "metering",
		MaxAttempts: 3,
	}
}

// DomainRenewalWorker checks for expiring domains and renews them.
type DomainRenewalWorker struct {
	river.WorkerDefaults[DomainRenewalJobArgs]
	DB              *pgxpool.Pool
	DomainRegistrar provider.DomainRegistrar
}

func (w *DomainRenewalWorker) Timeout(_ *river.Job[DomainRenewalJobArgs]) time.Duration {
	return 5 * time.Minute
}

func (w *DomainRenewalWorker) Work(ctx context.Context, _ *river.Job[DomainRenewalJobArgs]) error {
	logger := slog.With("job", "domain_renewal")

	// Find domains expiring within 30 days with auto-renew enabled.
	rows, err := w.DB.Query(ctx, `
		SELECT pd.id, pd.team_id, pd.domain, pd.renewal_price, pd.expires_at
		FROM purchased_domains pd
		WHERE pd.status = 'active'
		  AND pd.auto_renew = true
		  AND pd.expires_at <= NOW() + INTERVAL '30 days'
		  AND (pd.renewed_at IS NULL OR pd.renewed_at < pd.expires_at - INTERVAL '31 days')
	`)
	if err != nil {
		return fmt.Errorf("query expiring domains: %w", err)
	}
	defer rows.Close()

	type renewalCandidate struct {
		id           string
		teamID       string
		domain       string
		renewalPrice float64
		expiresAt    time.Time
	}

	var candidates []renewalCandidate
	for rows.Next() {
		var c renewalCandidate
		if err := rows.Scan(&c.id, &c.teamID, &c.domain, &c.renewalPrice, &c.expiresAt); err != nil {
			logger.Warn("scan error", "error", err)
			continue
		}
		candidates = append(candidates, c)
	}

	if len(candidates) == 0 {
		logger.Info("no domains need renewal")
		return nil
	}

	logger.Info("domains due for renewal", "count", len(candidates))

	var renewed, failed int
	for _, c := range candidates {
		err := w.renewOne(ctx, c.id, c.teamID, c.domain, c.renewalPrice)
		if err != nil {
			logger.Warn("renewal failed", "domain", c.domain, "error", err)
			failed++
		} else {
			logger.Info("domain renewed", "domain", c.domain)
			renewed++
		}
	}

	logger.Info("renewal cycle complete", "renewed", renewed, "failed", failed)
	return nil
}

func (w *DomainRenewalWorker) renewOne(ctx context.Context, domainID, teamID, domain string, renewalPrice float64) error {
	// Check and deduct balance atomically.
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var balance float64
	err = tx.QueryRow(ctx, "SELECT balance FROM teams WHERE id = $1 FOR UPDATE", teamID).Scan(&balance)
	if err != nil {
		return fmt.Errorf("check balance: %w", err)
	}

	if balance < renewalPrice {
		// Insufficient balance — mark as renewal_failed.
		_, _ = w.DB.Exec(ctx,
			"UPDATE purchased_domains SET status = 'renewal_failed', updated_at = NOW() WHERE id = $1",
			domainID,
		)
		// Audit log for alert.
		_, _ = w.DB.Exec(ctx, `
			INSERT INTO audit_logs (team_id, action, resource_type, resource_id, details)
			VALUES ($1, 'domain_renewal_failed', 'domain', $2, $3)
		`, teamID, domain, fmt.Sprintf(`{"reason":"insufficient_balance","balance":%.2f,"renewal_price":%.2f}`, balance, renewalPrice))
		return fmt.Errorf("insufficient balance: $%.2f < $%.2f", balance, renewalPrice)
	}

	// Deduct balance.
	_, err = tx.Exec(ctx, "UPDATE teams SET balance = balance - $1 WHERE id = $2", renewalPrice, teamID)
	if err != nil {
		return fmt.Errorf("deduct balance: %w", err)
	}

	// Record usage.
	_, err = tx.Exec(ctx, `
		INSERT INTO usage_records (team_id, resource_type, resource_id, quantity, unit, cost, recorded_at, provider, resource_urn)
		VALUES ($1, 'domain_renewal', $2, 1, 'years', $3, NOW(), 'namecom', $4)
	`, teamID, domain, renewalPrice, "namecom:renew:"+domain)
	if err != nil {
		return fmt.Errorf("record usage: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	// Call registrar API.
	result, err := w.DomainRegistrar.RenewDomain(ctx, domain, 1)
	if err != nil {
		// Refund on API failure.
		_, _ = w.DB.Exec(ctx, "UPDATE teams SET balance = balance + $1 WHERE id = $2", renewalPrice, teamID)
		_, _ = w.DB.Exec(ctx, `
			INSERT INTO usage_records (team_id, resource_type, resource_id, quantity, unit, cost, recorded_at, provider, resource_urn)
			VALUES ($1, 'domain_renewal_refund', $2, 1, 'refund', -$3, NOW(), 'namecom', $4)
		`, teamID, domain, renewalPrice, "namecom:renew_refund:"+domain)
		return fmt.Errorf("registrar renew: %w", err)
	}

	// Update expiry.
	_, _ = w.DB.Exec(ctx, `
		UPDATE purchased_domains SET
			expires_at = $1,
			renewed_at = NOW(),
			status = 'active',
			updated_at = NOW()
		WHERE id = $2
	`, result.ExpiresAt, domainID)

	return nil
}

// ═══════════════════════════════════════════════════════════════════
// Domain Reconciliation — integrity check against registrar
// ═══════════════════════════════════════════════════════════════════

// DomainReconcileJobArgs is the payload for the daily domain reconciliation.
type DomainReconcileJobArgs struct{}

func (DomainReconcileJobArgs) Kind() string { return "domain_reconcile" }

func (DomainReconcileJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       "metering",
		MaxAttempts: 3,
	}
}

// DomainReconcileWorker compares domains at the registrar with our DB
// and logs discrepancies as security alerts.
type DomainReconcileWorker struct {
	river.WorkerDefaults[DomainReconcileJobArgs]
	DB              *pgxpool.Pool
	DomainRegistrar provider.DomainRegistrar
}

func (w *DomainReconcileWorker) Timeout(_ *river.Job[DomainReconcileJobArgs]) time.Duration {
	return 5 * time.Minute
}

func (w *DomainReconcileWorker) Work(ctx context.Context, _ *river.Job[DomainReconcileJobArgs]) error {
	logger := slog.With("job", "domain_reconcile")

	// Fetch all domains from registrar.
	registrarDomains, err := w.DomainRegistrar.ListDomains(ctx)
	if err != nil {
		return fmt.Errorf("list registrar domains: %w", err)
	}

	registrarSet := make(map[string]provider.DomainInfo, len(registrarDomains))
	for _, d := range registrarDomains {
		registrarSet[d.Domain] = d
	}

	// Fetch all domains from our DB.
	rows, err := w.DB.Query(ctx, `
		SELECT domain, status, expires_at FROM purchased_domains WHERE status IN ('active', 'transfer_locked')
	`)
	if err != nil {
		return fmt.Errorf("query purchased_domains: %w", err)
	}
	defer rows.Close()

	dbSet := make(map[string]bool)
	for rows.Next() {
		var domain, status string
		var expiresAt time.Time
		if err := rows.Scan(&domain, &status, &expiresAt); err != nil {
			continue
		}
		dbSet[domain] = true

		info, inRegistrar := registrarSet[domain]
		if !inRegistrar {
			// Domain in our DB but NOT at registrar — possible unauthorized transfer.
			logger.Error("SECURITY ALERT: domain missing from registrar",
				"domain", domain, "db_status", status)
			w.writeSecurityAlert(ctx, domain, "domain_missing_from_registrar",
				"Domain exists in LaunchKit DB but not found at registrar. Possible unauthorized transfer.")
			continue
		}

		// Check if domain is unlocked (should always be locked).
		if !info.Locked {
			logger.Warn("domain unlocked at registrar", "domain", domain)
			w.writeSecurityAlert(ctx, domain, "domain_unlocked",
				"Domain registrar lock is disabled. Attempting to re-lock.")
			// Attempt to re-lock.
			if lockErr := w.DomainRegistrar.LockDomain(ctx, domain); lockErr != nil {
				logger.Error("failed to re-lock domain", "domain", domain, "error", lockErr)
			}
		}
	}

	// Check for domains at registrar NOT in our DB (untracked).
	for domain := range registrarSet {
		if !dbSet[domain] {
			logger.Warn("untracked domain at registrar", "domain", domain)
			w.writeSecurityAlert(ctx, domain, "untracked_domain",
				"Domain exists at registrar but not in LaunchKit DB. May be from a failed purchase or manual registration.")
		}
	}

	logger.Info("domain reconciliation complete",
		"registrar_count", len(registrarDomains),
		"db_count", len(dbSet),
	)
	return nil
}

func (w *DomainReconcileWorker) writeSecurityAlert(ctx context.Context, domain, action, details string) {
	_, _ = w.DB.Exec(ctx, `
		INSERT INTO audit_logs (team_id, action, resource_type, resource_id, details)
		VALUES (
			COALESCE((SELECT team_id FROM purchased_domains WHERE domain = $1 LIMIT 1), ''),
			$2, 'domain', $1, $3
		)
	`, domain, action, fmt.Sprintf(`{"domain":"%s","message":"%s"}`, domain, details))
}
