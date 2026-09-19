// leads-server is the L0 foundation server for the leads-engine app
// (HUI-1748): net/http + embedded sqlite migrations + platform identity.
//
// Subcommands:
//
//	(default)                      run the API server
//	provision-tenant               create a tenant (bootstrap/ops)
//	provision-member               add a member to a tenant (bootstrap/ops)
//	provision-grant                add an agent per-tenant grant (bootstrap/ops)
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/bianjiefilm/leads-engine/server/internal/config"
	"github.com/bianjiefilm/leads-engine/server/internal/httpapi"
	"github.com/bianjiefilm/leads-engine/server/internal/provision"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	args := os.Args[1:]
	switch {
	case len(args) > 0 && args[0] == "provision-tenant":
		cmdProvisionTenant(args[1:])
	case len(args) > 0 && args[0] == "provision-member":
		cmdProvisionMember(args[1:])
	case len(args) > 0 && args[0] == "provision-grant":
		cmdProvisionGrant(args[1:])
	default:
		cmdServe()
	}
}

func cmdServe() {
	cfg := config.FromEnv()
	srv, err := httpapi.Open(cfg, log.Default())
	if err != nil {
		log.Fatalf("leads-server: open: %v", err)
	}
	defer srv.Close()

	// 配置门:缺依赖时打印显式清单;服务仍启动(健康探针可观测),
	// 但一切鉴权动作将 fail-closed。
	if problems := cfg.Gate(); len(problems) > 0 {
		log.Printf("leads-server: CONFIG GATE NOT SATISFIED (%d):", len(problems))
		for _, p := range problems {
			log.Printf("  - %s", p)
		}
	}
	log.Printf("leads-server: starting %s identity=platform", cfg.Describe())

	h := srv.Handler()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serveHTTP(ctx, cfg.HTTPAddr, h); err != nil {
		log.Fatalf("leads-server: %v", err)
	}
	log.Printf("leads-server: shut down cleanly")
}

func cmdProvisionTenant(args []string) {
	fs := flag.NewFlagSet("provision-tenant", flag.ExitOnError)
	dbPath := fs.String("db", "", "sqlite db path (required)")
	name := fs.String("name", "", "tenant name (required)")
	_ = fs.Parse(args)
	if *dbPath == "" || *name == "" {
		fatalUsage("provision-tenant requires -db and -name")
	}
	id, err := provision.Tenant(*dbPath, *name)
	must(err)
	fmt.Println(id)
}

func cmdProvisionMember(args []string) {
	fs := flag.NewFlagSet("provision-member", flag.ExitOnError)
	dbPath := fs.String("db", "", "sqlite db path (required)")
	tenant := fs.String("tenant", "", "tenant id (required)")
	principal := fs.String("principal", "", "platform principal ref, usr_* (required)")
	role := fs.String("role", "sales", "owner|sales|agent")
	name := fs.String("name", "", "display name")
	disabled := fs.Bool("disabled", false, "create as disabled")
	_ = fs.Parse(args)
	if *dbPath == "" || *tenant == "" || *principal == "" {
		fatalUsage("provision-member requires -db, -tenant, -principal")
	}
	id, err := provision.Member(*dbPath, *tenant, *principal, *role, *name, !*disabled)
	must(err)
	fmt.Println(id)
}

func cmdProvisionGrant(args []string) {
	fs := flag.NewFlagSet("provision-grant", flag.ExitOnError)
	dbPath := fs.String("db", "", "sqlite db path (required)")
	tenant := fs.String("tenant", "", "tenant id (required)")
	principal := fs.String("principal", "", "platform principal ref, usr_* (required)")
	_ = fs.Parse(args)
	if *dbPath == "" || *tenant == "" || *principal == "" {
		fatalUsage("provision-grant requires -db, -tenant, -principal")
	}
	id, err := provision.Grant(*dbPath, *tenant, *principal)
	must(err)
	fmt.Println(id)
}

func fatalUsage(msg string) {
	fmt.Fprintln(os.Stderr, "leads-server: "+msg)
	os.Exit(2)
}

func must(err error) {
	if err != nil {
		log.Fatalf("leads-server: %v", err)
	}
}
