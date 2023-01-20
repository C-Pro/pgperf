package pgperf

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// Ineffective (but still common) way to get multiple records.
func GetUsers1(ctx context.Context, tx pgx.Tx, ids []int) ([]string, error) {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		var name string
		q := fmt.Sprintf("select name from test.users where id = %d", id)
		if err := tx.QueryRow(ctx, q).Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to select user %w", err)
		}

		names = append(names, name)
	}

	return names, nil
}

// Use bind parametes instead of string concatenation. Allows pgx to use prepared statement
// and is less prone to SQL injection attaks.
func GetUsers2(ctx context.Context, tx pgx.Tx, ids []int) ([]string, error) {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		var name string
		if err := tx.QueryRow(ctx, "select name from test.users where id = $1", id).Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to select user %w", err)
		}

		names = append(names, name)
	}

	return names, nil
}

// Use prepared statement to avoid parsing step in every query.
// Does not do something in case of PGX, because it is preparing statements internally
// anyway, so putting it here for demonstration only.
func GetUsers3(ctx context.Context, tx pgx.Tx, ids []int) ([]string, error) {
	names := make([]string, 0, len(ids))
	stmt, err := tx.Prepare(ctx, "superquery", "select name from test.users where id = $1")
	if err != nil {
		return nil, fmt.Errorf("failed to prepare statement: %w", err)
	}

	for _, id := range ids {
		var name string
		if err := tx.QueryRow(ctx, stmt.Name, id).Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to select user %w", err)
		}

		names = append(names, name)
	}

	return names, nil
}

// Get rid of loop and use single query returning multiple rows.
func GetUsers4(ctx context.Context, tx pgx.Tx, ids []int) ([]string, error) {
	names := make([]string, 0, len(ids))
	rows, err := tx.Query(ctx, "select name from test.users where id = any($1)", ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string

		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("failed to scan user name %w", err)
		}

		names = append(names, name)
	}

	return names, rows.Err()
}

// Simple insert in the loop (using bind variables)
func InsertUsers1(ctx context.Context, tx pgx.Tx, ids []int) error {
	for _, id := range ids {
		if _, err := tx.Exec(ctx, "insert into test.users(id, name) values ($1, $2)", id, fmt.Sprintf("user %d", id)); err != nil {
			return fmt.Errorf("failed to insert user %w", err)
		}
	}

	return nil
}

// Build one huge insert string using concatenation.
func InsertUsers2(ctx context.Context, tx pgx.Tx, ids []int) error {
	q := "insert into test.users(id,name) values "
	for _, id := range ids {
		q += fmt.Sprintf("(%d, 'user %d'),", id, id)
	}

	_, err := tx.Exec(ctx, q[:len(q)-1])

	return err
}

// Build one huge insert string using strings.Builder.
func InsertUsers3(ctx context.Context, tx pgx.Tx, ids []int) error {
	var sb strings.Builder
	sb.WriteString("insert into test.users(id,name) values ")
	for i, id := range ids {
		sb.WriteString(fmt.Sprintf("(%d, 'user %d')", id, id))
		if i < len(ids)-1 {
			sb.WriteRune(',')
		}
	}

	_, err := tx.Exec(ctx, sb.String())

	return err
}

// Build one huge insert string using strings.Builder and bind vars.
func InsertUsers4(ctx context.Context, tx pgx.Tx, ids []int) error {
	var (
		sb   strings.Builder
		args []interface{}
	)

	sb.WriteString("insert into test.users(id,name) values ")
	for i, id := range ids {
		sb.WriteString(fmt.Sprintf("($%d, $%d)", i*2+1, i*2+1+1))
		args = append(args, id, fmt.Sprintf("user %d", id))
		if i < len(ids)-1 {
			sb.WriteRune(',')
		}
	}

	_, err := tx.Exec(ctx, sb.String(), args...)

	return err
}

// Use pgx.Batch.
func InsertUsers5(ctx context.Context, tx pgx.Tx, ids []int) error {
	var b pgx.Batch
	for _, id := range ids {
		b.Queue("insert into test.users(id,name) values ($1, $2)", id, fmt.Sprintf("user %d", id))
	}

	br := tx.SendBatch(ctx, &b)
	_, err := br.Exec()
	br.Close()

	return err
}

// Use CopyFrom.
func InsertUsers6(ctx context.Context, tx pgx.Tx, ids []int) error {
	rows := make([][]interface{}, len(ids))
	for i, id := range ids {
		rows[i] = []interface{}{id, fmt.Sprintf("user %d", id)}
	}

	cnt, err := tx.CopyFrom(ctx, pgx.Identifier{"test", "users"}, []string{"id", "name"}, pgx.CopyFromRows(rows))
	if cnt != int64(len(ids)) {
		return fmt.Errorf("expected to copy %d rows, but got %d", len(ids), cnt)
	}

	return err
}

func TransferLock(ctx context.Context, tx pgx.Tx, from, to int, amt decimal.Decimal) error {
	if from == to {
		return errors.New("can't transfer to self")
	}
	var (
		srcAmount  decimal.Decimal
		destAmount decimal.Decimal
		nCurr      int
	)
	q := `select max(case when id = $1 then amount else null end) amount_from,
	             max(case when id = $2 then amount else null end) amount_to,
				 count(distinct currency)
			from (select * from test.accounts where id in($3,$4) for update) x`

	if err := tx.QueryRow(ctx, q, from, to, from, to).Scan(&srcAmount, &destAmount, &nCurr); err != nil {
		return fmt.Errorf("failed to lock accounts: %w", err)
	}

	if nCurr != 1 {
		return errors.New("can't transfer between different currencies")
	}

	if srcAmount.LessThan(amt) {
		return errors.New("not enough balance on source account")
	}

	r, err := tx.Exec(ctx, "update test.accounts set amount = amount - $1 where id = $2", amt, from)
	if err != nil {
		return err
	}

	if r.RowsAffected() != 1 {
		return sql.ErrNoRows
	}

	r, err = tx.Exec(ctx, "update test.accounts set amount = amount + $1 where id = $2", amt, to)
	if err != nil {
		return err
	}

	if r.RowsAffected() != 1 {
		return sql.ErrNoRows
	}

	return nil
}
