package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/shadatr/switchyard/internal/auth"
	"github.com/shadatr/switchyard/internal/config"
	"github.com/shadatr/switchyard/internal/store"
)

const keysUsage = `usage:
  switchyard keys create --project NAME --name NAME [--models a,b] [--rpm N] [--tpm N] [--budget USD]
  switchyard keys list
  switchyard keys disable --id KEY_ID`

func runKeys(args []string) error {
	if len(args) == 0 {
		return errors.New(keysUsage)
	}
	fs := flag.NewFlagSet("keys "+args[0], flag.ContinueOnError)
	configPath := fs.String("config", "deploy/switchyard.example.yaml", "path to YAML config")
	project := fs.String("project", "default", "project name")
	name := fs.String("name", "", "key name")
	models := fs.String("models", "", "comma-separated allowed model aliases (empty = all)")
	rpm := fs.Int("rpm", 0, "requests per minute limit (0 = unlimited)")
	tpm := fs.Int("tpm", 0, "tokens per minute limit (0 = unlimited)")
	budget := fs.Float64("budget", -1, "monthly budget in USD (-1 = unlimited)")
	id := fs.String("id", "", "key id")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	cfg, _, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	st, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	switch args[0] {
	case "create":
		if *name == "" {
			return errors.New("--name is required")
		}
		plain, prefix, hash, err := auth.Generate()
		if err != nil {
			return err
		}
		nk := store.NewKey{ProjectName: *project, Name: *name, Prefix: prefix, Hash: hash}
		if *models != "" {
			nk.AllowedModels = strings.Split(*models, ",")
		}
		if *rpm > 0 {
			nk.RPMLimit = rpm
		}
		if *tpm > 0 {
			nk.TPMLimit = tpm
		}
		if *budget >= 0 {
			nk.MonthlyBudgetUSD = budget
		}
		k, err := st.CreateKey(ctx, nk)
		if err != nil {
			return err
		}
		fmt.Printf("Created key %s (%s) in project %s.\n\n  %s\n\nStore it now — it cannot be shown again.\n", k.Name, k.ID, k.ProjectName, plain)
	case "list":
		keys, err := st.ListKeys(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tPROJECT\tNAME\tPREFIX\tMODELS\tRPM\tTPM\tBUDGET\tDISABLED\tLAST USED")
		for _, k := range keys {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s…\t%s\t%s\t%s\t%s\t%t\t%s\n", k.ID, k.ProjectName, k.Name, k.Prefix,
				orAll(k.AllowedModels), intOr(k.RPMLimit), intOr(k.TPMLimit), floatOr(k.MonthlyBudgetUSD), k.Disabled, timeOr(k.LastUsedAt))
		}
		return tw.Flush()
	case "disable":
		if *id == "" {
			return errors.New("--id is required")
		}
		disabled := true
		if _, err := st.UpdateKey(ctx, *id, store.KeyUpdate{Disabled: &disabled}); err != nil {
			return err
		}
		fmt.Println("Key disabled. Gateways pick this up within the auth cache TTL (30s).")
	default:
		return errors.New(keysUsage)
	}
	return nil
}

func orAll(m []string) string {
	if len(m) == 0 {
		return "*"
	}
	return strings.Join(m, ",")
}

func intOr(v *int) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprint(*v)
}

func floatOr(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("$%.2f", *v)
}

func timeOr(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return t.Format(time.RFC3339)
}
