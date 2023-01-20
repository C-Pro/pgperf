package pgperf_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"testing"

	"pgperf"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

const batchSize = 100

var (
	pool   *pgxpool.Pool
	ctx    context.Context
	cancel context.CancelFunc
)

func runTests(m *testing.M) int {
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	var err error
	pool, err = pgxpool.New(ctx, "postgres://postgres:postgres@localhost/postgres?sslmode=disable")
	if err != nil {
		panic(err)
	}

	defer pool.Close()

	return m.Run()
}

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func getConn(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func getTx(ctx context.Context) (pgx.Tx, func(), error) {
	conn, err := getConn(ctx)
	if err != nil {
		return nil, nil, err
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, conn.Release, err
	}

	return tx, conn.Release, nil
}

func runGetUsers(b *testing.B, variant int) {
	tx, close, err := getTx(ctx)
	if close != nil {
		defer close()
	}

	if err != nil {
		b.Fatalf("failed to start transaction : %v", err)
	}

	defer tx.Rollback(ctx)

	var f func(context.Context, pgx.Tx, []int) ([]string, error)
	switch variant {
	case 1:
		f = pgperf.GetUsers1
	case 2:
		f = pgperf.GetUsers2
	case 3:
		f = pgperf.GetUsers3
	case 4:
		f = pgperf.GetUsers4
	default:
		b.Fatalf("unknown GetUsers variant %d", variant)
	}

	ids := make([]int, batchSize)
	for i := 0; i < b.N; i++ {
		for j := 0; j < len(ids); j++ {
			ids[j] = rand.Intn(1000000)
		}

		if _, err := f(ctx, tx, ids); err != nil {
			b.Fatalf("failed to call GetUsers: %v", err)
		}
	}
}

func BenchmarkGetUsers1(b *testing.B) {
	runGetUsers(b, 1)
}

func BenchmarkGetUsers2(b *testing.B) {
	runGetUsers(b, 2)
}

func BenchmarkGetUsers3(b *testing.B) {
	runGetUsers(b, 3)
}

func BenchmarkGetUsers4(b *testing.B) {
	runGetUsers(b, 4)
}

func runInsertUsers(b *testing.B, variant int) {
	conn, err := getConn(ctx)
	if err != nil {
		b.Fatalf("failed to aqcuire connection: %v", err)
	}
	defer conn.Release()

	var f func(context.Context, pgx.Tx, []int) error
	switch variant {
	case 1:
		f = pgperf.InsertUsers1
	case 2:
		f = pgperf.InsertUsers2
	case 3:
		f = pgperf.InsertUsers3
	case 4:
		f = pgperf.InsertUsers4
	case 5:
		f = pgperf.InsertUsers5
	case 6:
		f = pgperf.InsertUsers6
	default:
		b.Fatalf("unknown InsertUsers variant %d", variant)
	}

	ids := make([]int, batchSize)
	for i := 0; i < b.N; i++ {
		for j := 0; j < len(ids); j++ {
			ids[j] = 1000001 + j
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			b.Fatalf("failed to start transaction: %v", err)
		}

		if err := f(ctx, tx, ids); err != nil {
			tx.Rollback(ctx)
			b.Fatalf("failed to call InsertUsers: %v", err)
		}

		tx.Rollback(ctx)
	}
}

func BenchmarkInsertUsers1(b *testing.B) {
	runInsertUsers(b, 1)
}

func BenchmarkInsertUsers2(b *testing.B) {
	runInsertUsers(b, 2)
}

func BenchmarkInsertUsers3(b *testing.B) {
	runInsertUsers(b, 3)
}

func BenchmarkInsertUsers4(b *testing.B) {
	runInsertUsers(b, 4)
}

func BenchmarkInsertUsers5(b *testing.B) {
	runInsertUsers(b, 5)
}

func BenchmarkInsertUsers6(b *testing.B) {
	runInsertUsers(b, 6)
}

func doTrx(ctx context.Context, conn *pgxpool.Conn, from, to, amount int) {
	amt := decimal.NewFromInt(int64(amount))
	tx, err := conn.Begin(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		panic(err)
	}

	defer tx.Rollback(ctx)

	// ctx, cancel := context.WithTimeout(ctx, time.Second)
	// defer cancel()
	if err := pgperf.TransferLock(ctx, tx, from, to, amt); err != nil {
		return
	}

	tx.Commit(ctx)
}

func BenchmarkTransferLock(b *testing.B) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	conn, err := getConn(ctx)
	if err != nil {
		b.Fatalf("failed to acquire connection: %v", err)
	}

	defer conn.Release()

	var totalIDRTbefore decimal.Decimal
	if err := conn.QueryRow(ctx, "select sum(amount) from test.accounts where currency = 'IDRT'").Scan(&totalIDRTbefore); err != nil {
		b.Fatalf("failed to get total IDRT: %v", err)
	}

	var ids []int
	q := `select array_agg(id)
	from test.accounts
	where currency = 'IDRT'
	and amount > 10000000`
	if err := conn.QueryRow(ctx, q).Scan(&ids); err != nil {
		b.Fatalf("failed to get IDRT accounts: %v", err)
	}

	for i := 0; i < 8; i++ {
		go func() {
			conn, err := getConn(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				panic(fmt.Errorf("failed to acquire connection: %v", err))
			}
			defer conn.Release()
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				from := ids[rand.Intn(len(ids))]
				to := ids[rand.Intn(len(ids))]
				amt := rand.Intn(10)
				doTrx(ctx, conn, from, to, amt)
			}
		}()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		from := ids[rand.Intn(len(ids))]
		to := ids[rand.Intn(len(ids))]
		amt := rand.Intn(10)
		doTrx(ctx, conn, from, to, amt)
	}

	var totalIDRTafter decimal.Decimal
	if err := conn.QueryRow(ctx, "select sum(amount) from test.accounts where currency = 'IDRT'").Scan(&totalIDRTafter); err != nil {
		b.Fatalf("failed to get total IDRT: %v", err)
	}

	if !totalIDRTbefore.Equal(totalIDRTafter) {
		b.Fatalf("total IDRT amount changed (before/after) %v/%v", totalIDRTbefore, totalIDRTafter)
	}
}
