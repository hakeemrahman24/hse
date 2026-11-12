// Command staffctl manages staff accounts from the command line. It is how
// the first admin is created on a new server, before anyone can log in.
//
//	staffctl add <username> "<Display Name>" <role,role> [email]
//	staffctl pin <username>
//	staffctl unlock <username>
//	staffctl off|on <username>
//
// The PIN is read from the STAFF_PIN variable, or from one line on standard
// input, so it never appears in the command line or the shell history.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"entry-permit/internal/config"
	"entry-permit/internal/dates"
	"entry-permit/internal/db"
	"entry-permit/internal/policy"
	"entry-permit/internal/staff"
	"entry-permit/migrations"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "staffctl:", err)
		os.Exit(1)
	}
}

func readPIN() (string, error) {
	if v := os.Getenv("STAFF_PIN"); v != "" {
		return v, nil
	}
	fmt.Fprint(os.Stderr, "PIN (4 to 8 letters or digits): ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read PIN: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func run(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: staffctl add|pin|unlock|on|off <username> ...")
	}
	cfg := config.Load(os.Getenv)
	database, err := db.Open(cfg.DBDriver, cfg.DSN())
	if err != nil {
		return err
	}
	defer database.Close()
	if err := db.Migrate(database, migrations.FS, migrations.Dir(cfg.DBDriver)); err != nil {
		return err
	}
	store := staff.NewStore(database, dates.SystemClock{}, policy.Default())
	ctx := context.Background()
	cmd, username := args[0], args[1]

	switch cmd {
	case "add":
		if len(args) < 4 {
			return fmt.Errorf("usage: staffctl add <username> \"<Display Name>\" <role,role> [email]")
		}
		roles, err := staff.ParseRoles(args[3])
		if err != nil {
			return err
		}
		email := ""
		if len(args) > 4 {
			email = args[4]
		}
		pin, err := readPIN()
		if err != nil {
			return err
		}
		u, err := store.Create(ctx, username, args[2], email, pin, roles)
		if err != nil {
			return err
		}
		fmt.Printf("created %s (%s) with roles %v\n", u.Username, u.DisplayName, u.Roles)
	case "pin":
		pin, err := readPIN()
		if err != nil {
			return err
		}
		if err := store.SetPIN(ctx, username, pin); err != nil {
			return err
		}
		fmt.Println("PIN changed and lockout cleared for", username)
	case "unlock":
		if err := store.Unlock(ctx, username); err != nil {
			return err
		}
		fmt.Println("unlocked", username)
	case "on", "off":
		if err := store.SetActive(ctx, username, cmd == "on"); err != nil {
			return err
		}
		fmt.Println(username, "is now", cmd)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
	return nil
}
