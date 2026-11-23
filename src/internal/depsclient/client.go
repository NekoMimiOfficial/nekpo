package depsclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"nekpo/internal/archive"
	"nekpo/internal/proto"
	"nekpo/internal/ui"
)

type Client struct {
	Server string
	HTTP   *http.Client
}

func CheckServer(server string, insecure bool) error {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("server must be a URL without a path, got %q", server)
	}
	h := u.Hostname()
	local := h == "localhost" || h == "127.0.0.1" || h == "::1"
	if u.Scheme != "https" && !local && !insecure && os.Getenv("NEKPO_ALLOW_INSECURE") != "1" {
		return fmt.Errorf("server %q is not https; set allow_insecure = true in the config or NEKPO_ALLOW_INSECURE=1 to allow it", server)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("server must be http or https, got %q", server)
	}
	return nil
}

func New(server string) *Client {
	return &Client{Server: strings.TrimRight(server, "/"), HTTP: &http.Client{}}
}

func depPath(lang, arch, name, version string) string {
	return "/api/v1/deps/" + url.PathEscape(lang) + "/" + url.PathEscape(arch) + "/" + url.PathEscape(name) + "/" + url.PathEscape(version)
}

func decodeErr(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var e struct{ Error string }
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return fmt.Errorf("server said %d: %s", resp.StatusCode, e.Error)
	}
	return fmt.Errorf("server said %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

func (c *Client) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Server+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, decodeErr(resp)
	}
	return resp, nil
}

func (c *Client) List(ctx context.Context, lang, arch, name string) ([]proto.DepInfo, error) {
	q := url.Values{}
	if lang != "" {
		q.Set("lang", lang)
	}
	if arch != "" {
		q.Set("arch", arch)
	}
	if name != "" {
		q.Set("name", name)
	}
	resp, err := c.get(ctx, "/api/v1/deps?"+q.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Deps []proto.DepInfo `json:"deps"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return out.Deps, nil
}

func (c *Client) Info(ctx context.Context, lang, arch, name, version string) (*proto.DepInfo, error) {
	if version == "" {
		version = "latest"
	}
	resp, err := c.get(ctx, depPath(lang, arch, name, version))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var info proto.DepInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

func (c *Client) Download(ctx context.Context, info *proto.DepInfo, dest string, p *ui.Progress) error {
	resp, err := c.get(ctx, depPath(info.Lang, info.Arch, info.Name, info.Version)+"/download")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	rd := &ui.Reader{R: resp.Body, Total: info.Size, P: p}
	_, err = io.Copy(out, rd)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dest)
		return err
	}
	sum, size, err := archive.SumFile(dest)
	if err != nil {
		return err
	}
	if sum != info.SHA256 || size != info.Size {
		os.Remove(dest)
		return fmt.Errorf("%s %s: downloaded file does not match the server's checksum", info.Name, info.Version)
	}
	return nil
}

func sign(req *http.Request, id proto.Identity, ts int64, sum string) {
	req.Header.Set(proto.HeaderKey, id.ID)
	req.Header.Set(proto.HeaderTime, strconv.FormatInt(ts, 10))
	req.Header.Set(proto.HeaderSig, proto.Sign(id.Key, req.Method, req.URL.Path, ts, sum))
}

func (c *Client) send(req *http.Request) (string, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", decodeErr(resp)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return strings.TrimSpace(string(body)), nil
}

func (c *Client) Push(ctx context.Context, id proto.Identity, lang, arch, name, version, file string, replace bool, note string, p *ui.Progress) (string, error) {
	sum, size, err := archive.SumFile(file)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	m := proto.DepManifest{Schema: 1, Builder: id.ID, Issued: now, Lang: lang, Arch: arch, Name: name, Version: version,
		File: proto.File{Name: filepath.Base(file), Size: size, SHA256: sum}, Replace: replace, Note: note}
	if err := m.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	src, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer src.Close()
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		err := func() error {
			w, err := mw.CreateFormField("manifest")
			if err != nil {
				return err
			}
			if _, err := w.Write(raw); err != nil {
				return err
			}
			part, err := mw.CreateFormFile("dep", m.File.Name)
			if err != nil {
				return err
			}
			if _, err := io.Copy(part, &ui.Reader{R: src, Total: size, P: p}); err != nil {
				return err
			}
			return mw.Close()
		}()
		pw.CloseWithError(err)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Server+depPath(lang, arch, name, version), pr)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	sign(req, id, now, proto.SumHex(raw))
	return c.send(req)
}

func (c *Client) Remove(ctx context.Context, id proto.Identity, lang, arch, name, version string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.Server+depPath(lang, arch, name, version), nil)
	if err != nil {
		return "", err
	}
	sign(req, id, time.Now().Unix(), proto.SumHex(nil))
	return c.send(req)
}
