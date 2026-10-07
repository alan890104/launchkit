package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"testing"

	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var _ func(context.Context, *pgxpool.Pool, provider.ConsumptionRecord, float64, float64, string, string, *slog.Logger) (bool, float64, error) = recordAndDeduct

type stubTx struct {
	queryRow func(ctx context.Context, sql string, args ...any) pgx.Row
}

func (tx *stubTx) Begin(context.Context) (pgx.Tx, error) { return nil, errors.New("not implemented") }
func (tx *stubTx) Commit(context.Context) error          { return nil }
func (tx *stubTx) Rollback(context.Context) error        { return nil }
func (tx *stubTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("not implemented")
}
func (tx *stubTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (tx *stubTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (tx *stubTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("not implemented")
}
func (tx *stubTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("not implemented")
}
func (tx *stubTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("not implemented")
}
func (tx *stubTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if tx.queryRow == nil {
		return stubRow{err: fmt.Errorf("unexpected QueryRow call: %s", sql)}
	}
	return tx.queryRow(ctx, sql, args...)
}
func (tx *stubTx) Conn() *pgx.Conn { return nil }

type stubRow struct {
	values []any
	err    error
}

func (r stubRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan destination count = %d, want %d", len(dest), len(r.values))
	}
	for i := range dest {
		if err := assignScanValue(dest[i], r.values[i]); err != nil {
			return err
		}
	}
	return nil
}

func assignScanValue(dest any, value any) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Pointer || dv.IsNil() {
		return fmt.Errorf("destination must be a non-nil pointer, got %T", dest)
	}
	v := reflect.ValueOf(value)
	target := dv.Elem()
	if !v.IsValid() {
		target.SetZero()
		return nil
	}
	if v.Type().AssignableTo(target.Type()) {
		target.Set(v)
		return nil
	}
	if v.Type().ConvertibleTo(target.Type()) {
		target.Set(v.Convert(target.Type()))
		return nil
	}
	return fmt.Errorf("cannot scan %T into %T", value, dest)
}

type legacyEgressLedger struct {
	egressUsageBytes int64
	usageRecords     int
	chargedUSD       float64
}

func (l *legacyEgressLedger) calculateEgressCost(deltaBytes int64, ratePerGB float64) float64 {
	l.egressUsageBytes += deltaBytes
	return float64(deltaBytes) / 1_073_741_824 * ratePerGB
}

func (l *legacyEgressLedger) failedRecordAndDeduct(float64) error {
	return errors.New("usage_records insert failed")
}

func TestEgressCostInTx_ReturnsZeroForNonPositiveBytes(t *testing.T) {
	t.Run("zero bytes short-circuits", func(t *testing.T) {
		tx := &stubTx{
			queryRow: func(context.Context, string, ...any) pgx.Row {
				t.Fatal("QueryRow should not be called when egressBytes == 0")
				return nil
			},
		}

		if got := EgressCostInTx(context.Background(), tx, "team-1", "proj-1", 0, PlanConfig[PlanStarter]); got != 0 {
			t.Fatalf("EgressCostInTx(..., 0, ...) = %v, want 0", got)
		}
	})

	t.Run("negative bytes short-circuits", func(t *testing.T) {
		tx := &stubTx{
			queryRow: func(context.Context, string, ...any) pgx.Row {
				t.Fatal("QueryRow should not be called when egressBytes < 0")
				return nil
			},
		}

		if got := EgressCostInTx(context.Background(), tx, "team-1", "proj-1", -1, PlanConfig[PlanStarter]); got != 0 {
			t.Fatalf("EgressCostInTx(..., -1, ...) = %v, want 0", got)
		}
	})
}

func TestEgressCostInTx_ChargesFullOverageWhenProjectFreeTierIsZero(t *testing.T) {
	const oneGiB = int64(1_073_741_824)

	queryCount := 0
	tx := &stubTx{
		queryRow: func(ctx context.Context, sql string, args ...any) pgx.Row {
			queryCount++
			switch queryCount {
			case 1:
				return stubRow{values: []any{int64(0), oneGiB}}
			case 2:
				return stubRow{values: []any{int64(0)}}
			default:
				return stubRow{err: fmt.Errorf("unexpected QueryRow call %d: %s", queryCount, sql)}
			}
		},
	}

	limits := PlanLimits{
		EgressFreePerProject: 0,
		EgressCapPerTeam:     0,
		EgressOveragePerGB:   0.12,
	}

	got := EgressCostInTx(context.Background(), tx, "team-1", "proj-1", float64(oneGiB), limits)
	want := limits.EgressOveragePerGB
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("EgressCostInTx full-overage charge = %v, want %v", got, want)
	}
}

func TestLegacySeparateEgressAccountingCanAdvanceUsageWithoutCharge(t *testing.T) {
	const oneGiB = int64(1_073_741_824)

	ledger := &legacyEgressLedger{}
	cost := ledger.calculateEgressCost(oneGiB, PlanConfig[PlanNone].EgressOveragePerGB)
	if cost <= 0 {
		t.Fatalf("legacy calculateEgressCost returned %v, want positive charge", cost)
	}

	err := ledger.failedRecordAndDeduct(cost)
	if err == nil {
		t.Fatal("failedRecordAndDeduct error = nil, want failure")
	}

	if ledger.egressUsageBytes != oneGiB {
		t.Fatalf("egress usage bytes = %d, want %d after separate egress transaction", ledger.egressUsageBytes, oneGiB)
	}
	if ledger.usageRecords != 0 {
		t.Fatalf("usage records = %d, want 0 after failed charge path", ledger.usageRecords)
	}
	if ledger.chargedUSD != 0 {
		t.Fatalf("charged USD = %v, want 0 after failed charge path", ledger.chargedUSD)
	}
}
