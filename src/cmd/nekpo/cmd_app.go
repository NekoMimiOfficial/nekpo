package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nekpo/internal/builder"
	"nekpo/internal/proto"
	"nekpo/internal/ui"
)

const appUsage = `usage: nekpo app <command> [-config builder.conf]

  list                                      apps on the store
  versions <id>                             kept versions of an app
  edit <id> [-name ..] [-tagline ..] [-summary ..] [-description ..] [-description-file f]
            [-author ..] [-license ..] [-website ..] [-source ..] [-category ..]
            [-accent ..] [-tags a,b] [-changelog-file f]
  asset <id> <icon|banner|shot-N> <file>    replace or add an image
  rm-asset <id> <file-name>                 remove an image
  rm-artifact <id> <file-name>              remove a download
  rollback <id> <version>                   make a kept version live again
  unpublish <id>                            remove the app from the store
`

func signedCall(ctx context.Context, r *builder.Runner, method, path string, body []byte, ctype string) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.Cfg.Server+path, rd)
	if err != nil {
		return nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	ts := time.Now().Unix()
	req.Header.Set(proto.HeaderKey, r.ID.ID)
	req.Header.Set(proto.HeaderTime, fmt.Sprint(ts))
	req.Header.Set(proto.HeaderSig, proto.Sign(r.ID.Key, method, path, ts, proto.SumHex(body)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error string }
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return nil, fmt.Errorf("server said %d: %s", resp.StatusCode, e.Error)
		}
		return nil, fmt.Errorf("server said %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func cmdApp(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, appUsage)
		return errors.New("app needs a command")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list", "ls":
		return appList(ctx, rest)
	case "versions":
		return appVersions(ctx, rest)
	case "edit":
		return appEdit(ctx, rest)
	case "asset":
		return appAsset(ctx, rest)
	case "rm-asset":
		return appRemove(ctx, rest, "assets", "image")
	case "rm-artifact":
		return appRemove(ctx, rest, "artifacts", "download")
	case "rollback":
		return appRollback(ctx, rest)
	case "unpublish":
		return appUnpublish(ctx, rest)
	case "help", "-h", "--help":
		fmt.Print(appUsage)
		return nil
	}
	fmt.Fprint(os.Stderr, appUsage)
	return fmt.Errorf("unknown app command %q", sub)
}

func appList(ctx context.Context, args []string) error {
	fs := newFlags("app list")
	cfg := fs.String("config", "", "builder config")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	r, err := newRunner(*cfg)
	if err != nil {
		return err
	}
	data, err := signedCall(ctx, r, http.MethodGet, "/api/v1/manage/apps", nil, "")
	if err != nil {
		return err
	}
	var out struct {
		Apps []struct {
			ID        string   `json:"id"`
			Name      string   `json:"name"`
			Version   string   `json:"version"`
			Published int64    `json:"published"`
			Downloads int64    `json:"downloads"`
			Artifacts int      `json:"artifacts"`
			Versions  []string `json:"versions"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	if len(out.Apps) == 0 {
		ui.Info("the store has no apps yet")
		return nil
	}
	var rows [][]string
	for _, a := range out.Apps {
		rows = append(rows, []string{a.ID, a.Name, a.Version, fmt.Sprint(a.Artifacts), fmt.Sprint(len(a.Versions)), fmt.Sprint(a.Downloads), time.Unix(a.Published, 0).Format("2006-01-02")})
	}
	ui.Table([]string{"ID", "NAME", "VERSION", "FILES", "KEPT", "DOWNLOADS", "PUBLISHED"}, rows)
	return nil
}

func appVersions(ctx context.Context, args []string) error {
	fs := newFlags("app versions")
	cfg := fs.String("config", "", "builder config")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: nekpo app versions <id>")
	}
	r, err := newRunner(*cfg)
	if err != nil {
		return err
	}
	data, err := signedCall(ctx, r, http.MethodGet, "/api/v1/manage/apps/"+url.PathEscape(pos[0])+"/versions", nil, "")
	if err != nil {
		return err
	}
	var out struct {
		Keep     int `json:"keep"`
		Versions []struct {
			Version    string   `json:"version"`
			Published  int64    `json:"published"`
			Current    bool     `json:"current"`
			Restorable bool     `json:"restorable"`
			Artifacts  []string `json:"artifacts"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	var rows [][]string
	for _, v := range out.Versions {
		state := ""
		switch {
		case v.Current:
			state = ui.Green("live")
		case v.Restorable:
			state = "can roll back"
		default:
			state = ui.Dim("files missing")
		}
		rows = append(rows, []string{v.Version, time.Unix(v.Published, 0).Format("2006-01-02 15:04"), fmt.Sprint(len(v.Artifacts)), state})
	}
	ui.Table([]string{"VERSION", "PUBLISHED", "FILES", "STATE"}, rows)
	ui.Note("the server keeps up to %d versions per app (keep_versions in store.conf)", out.Keep)
	return nil
}

func appEdit(ctx context.Context, args []string) error {
	fs := newFlags("app edit")
	cfg := fs.String("config", "", "builder config")
	text := map[string]*string{}
	for _, k := range []string{"name", "tagline", "summary", "description", "author", "license", "website", "source", "category", "accent"} {
		text[k] = fs.String(k, "", "new "+k)
	}
	descFile := fs.String("description-file", "", "read the description from a file")
	changelogFile := fs.String("changelog-file", "", "read the changelog from a file")
	tags := fs.String("tags", "", "comma separated tags; use - to clear")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: nekpo app edit <id> [flags]")
	}
	body := map[string]any{}
	fs.Visit(func(f *flag.Flag) {
		if p, ok := text[f.Name]; ok {
			body[f.Name] = *p
		}
	})
	readInto := func(path, key string) error {
		if path == "" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		body[key] = strings.TrimSpace(string(raw))
		return nil
	}
	if err := readInto(*descFile, "description"); err != nil {
		return err
	}
	if err := readInto(*changelogFile, "changelog"); err != nil {
		return err
	}
	if *tags != "" {
		list := []string{}
		if *tags != "-" {
			for _, t := range strings.Split(*tags, ",") {
				if t = strings.TrimSpace(t); t != "" {
					list = append(list, t)
				}
			}
		}
		body["tags"] = list
	}
	if len(body) == 0 {
		return errors.New("nothing to change; pass at least one flag")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	r, err := newRunner(*cfg)
	if err != nil {
		return err
	}
	if _, err := signedCall(ctx, r, http.MethodPatch, "/api/v1/manage/apps/"+url.PathEscape(pos[0]), raw, "application/json"); err != nil {
		return err
	}
	ui.OK("updated %s", pos[0])
	return nil
}

func appAsset(ctx context.Context, args []string) error {
	fs := newFlags("app asset")
	cfg := fs.String("config", "", "builder config")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 3 {
		return errors.New("usage: nekpo app asset <id> <icon|banner|shot-N> <file>")
	}
	ext := strings.ToLower(filepath.Ext(pos[2]))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".svg":
	default:
		return errors.New("assets must be png, jpg, webp, gif or svg images")
	}
	data, err := os.ReadFile(pos[2])
	if err != nil {
		return err
	}
	if len(data) > 10<<20 {
		return errors.New("assets are limited to 10 MiB")
	}
	name := pos[1] + ext
	r, err := newRunner(*cfg)
	if err != nil {
		return err
	}
	if _, err := signedCall(ctx, r, http.MethodPut, "/api/v1/manage/apps/"+url.PathEscape(pos[0])+"/assets/"+url.PathEscape(name), data, "application/octet-stream"); err != nil {
		return err
	}
	ui.OK("%s set on %s", name, pos[0])
	return nil
}

func appRemove(ctx context.Context, args []string, kind, what string) error {
	fs := newFlags("app rm")
	cfg := fs.String("config", "", "builder config")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return fmt.Errorf("usage: nekpo app rm-%s <id> <file-name>", strings.TrimSuffix(kind, "s"))
	}
	if !*yes && !ui.Confirm(fmt.Sprintf("Remove the %s %s from %s?", what, pos[1], pos[0]), false) {
		return errors.New("cancelled")
	}
	r, err := newRunner(*cfg)
	if err != nil {
		return err
	}
	if _, err := signedCall(ctx, r, http.MethodDelete, "/api/v1/manage/apps/"+url.PathEscape(pos[0])+"/"+kind+"/"+url.PathEscape(pos[1]), nil, ""); err != nil {
		return err
	}
	ui.OK("removed %s", pos[1])
	return nil
}

func appRollback(ctx context.Context, args []string) error {
	fs := newFlags("app rollback")
	cfg := fs.String("config", "", "builder config")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return errors.New("usage: nekpo app rollback <id> <version>")
	}
	if !*yes && !ui.Confirm(fmt.Sprintf("Make %s %s the live version?", pos[0], pos[1]), false) {
		return errors.New("cancelled")
	}
	r, err := newRunner(*cfg)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(map[string]string{"version": pos[1]})
	if _, err := signedCall(ctx, r, http.MethodPost, "/api/v1/manage/apps/"+url.PathEscape(pos[0])+"/rollback", raw, "application/json"); err != nil {
		return err
	}
	ui.OK("%s is now at %s", pos[0], pos[1])
	return nil
}

func appUnpublish(ctx context.Context, args []string) error {
	fs := newFlags("app unpublish")
	cfg := fs.String("config", "", "builder config")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: nekpo app unpublish <id>")
	}
	if !*yes && !ui.Confirm(fmt.Sprintf("Remove %s and all its files from the store?", pos[0]), false) {
		return errors.New("cancelled")
	}
	r, err := newRunner(*cfg)
	if err != nil {
		return err
	}
	out, err := builder.Unpublish(ctx, r.Cfg.Server, r.ID, pos[0])
	if err != nil {
		return err
	}
	ui.OK("%s", out)
	return nil
}
