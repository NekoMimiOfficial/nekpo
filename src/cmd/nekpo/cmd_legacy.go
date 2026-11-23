package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"nekpo/internal/proto"
	"nekpo/internal/ui"
)

func cmdRunLoop(ctx context.Context, args []string) error {
	fs := newFlags("run")
	cfg := fs.String("config", "", "builder config")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	r, err := newRunner(*cfg)
	if err != nil {
		return err
	}
	log.Printf("watching %d repositories, key %s", len(r.Cfg.Repos), r.ID.ID)
	r.Loop(ctx)
	return nil
}

func cmdOnce(ctx context.Context, args []string) error {
	fs := newFlags("once")
	cfg := fs.String("config", "", "builder config")
	force := fs.Bool("force", false, "rebuild even if nothing changed")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	only := map[string]bool{}
	for _, n := range pos {
		only[n] = true
	}
	r, err := newRunner(*cfg)
	if err != nil {
		return err
	}
	if n := r.Once(ctx, *force, only); n > 0 {
		return fmt.Errorf("%d repositories failed", n)
	}
	return nil
}

func cmdKeygen(args []string) error {
	fs := newFlags("keygen")
	id := fs.String("id", "builder1", "key id")
	out := fs.String("out", "builder.key", "private key file to create")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	if _, err := os.Stat(*out); err == nil {
		return errors.New(*out + " already exists")
	}
	ident, err := proto.GenerateIdentity(*id)
	if err != nil {
		return err
	}
	if err := ident.Save(*out); err != nil {
		return err
	}
	ui.OK("private key written to %s", *out)
	fmt.Printf("add this line to the store's authorized_keys:\n\n%s\n", ident.PublicLine())
	return nil
}
