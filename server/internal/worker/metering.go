package worker

import (
	"context"
	"time"

	"github.com/alan890104/launchkit/server/internal/billing"
	"github.com/alan890104/launchkit/server/internal/provider"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// MeteringJobArgs is the payload for the hourly metering job.
type MeteringJobArgs struct {
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
}

func (MeteringJobArgs) Kind() string { return "metering" }

func (MeteringJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       "metering",
		MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
		},
	}
}

// MeteringWorker runs the hourly billing metering cycle.
type MeteringWorker struct {
	river.WorkerDefaults[MeteringJobArgs]
	DB       *pgxpool.Pool
	Fetchers []provider.ConsumptionFetcher
	Pricing  *billing.PricingTable
	Compute  provider.Compute
}

func (w *MeteringWorker) Timeout(_ *river.Job[MeteringJobArgs]) time.Duration {
	return 10 * time.Minute
}

func (w *MeteringWorker) Work(ctx context.Context, job *river.Job[MeteringJobArgs]) error {
	return billing.RunMeteringCycle(ctx, billing.MeteringConfig{
		DB:       w.DB,
		Fetchers: w.Fetchers,
		Pricing:  w.Pricing,
		Compute:  w.Compute,
	}, job.Args.PeriodStart, job.Args.PeriodEnd)
}
