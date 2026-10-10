// oap-admin is an offline recovery tool; passwords are read only from stdin.
package main

import (
	"context"
	"fmt"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
	"io"
	"os"
	"strings"
	"time"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 2 || os.Args[1] != "reset-owner-password" {
		return fmt.Errorf("usage: oap-admin reset-owner-password < password-file")
	}
	raw, e := io.ReadAll(io.LimitReader(os.Stdin, 258))
	if e != nil {
		return e
	}
	password := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	hash, e := access.Password(password)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, e := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if e != nil {
		return fmt.Errorf("cannot connect to the installation database")
	}
	defer s.Pool.Close()
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var id string
	if e = tx.QueryRow(ctx, "UPDATE oap_users SET password_hash=$1 WHERE role='owner' RETURNING id", hash).Scan(&id); e != nil {
		return fmt.Errorf("owner account not found")
	}
	if _, e = tx.Exec(ctx, "UPDATE oap_credentials SET revoked_at=now() WHERE user_id=$1", id); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO oap_audit(id,user_id,action,status,completed_at) VALUES($1,$2,'owner.password-recovery',200,now())", access.Secret(), id); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	fmt.Println("Owner password reset; all owner sessions and agent credentials revoked.")
	return nil
}
