package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"nekpo/internal/proto"
)

func sign(req *http.Request, id proto.Identity, ts int64, bodySum string) {
	req.Header.Set(proto.HeaderKey, id.ID)
	req.Header.Set(proto.HeaderTime, strconv.FormatInt(ts, 10))
	req.Header.Set(proto.HeaderSig, proto.Sign(id.Key, req.Method, req.URL.Path, ts, bodySum))
}

func Publish(ctx context.Context, server string, id proto.Identity, res *Result) (string, error) {
	now := time.Now().Unix()
	res.Manifest.Builder = id.ID
	res.Manifest.Issued = now
	raw, err := json.Marshal(res.Manifest)
	if err != nil {
		return "", err
	}
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
			for _, u := range res.Uploads {
				f, err := os.Open(u.Path)
				if err != nil {
					return err
				}
				part, err := mw.CreateFormFile(u.Field, u.Name)
				if err == nil {
					_, err = io.Copy(part, f)
				}
				f.Close()
				if err != nil {
					return err
				}
			}
			return mw.Close()
		}()
		pw.CloseWithError(err)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/v1/publish", pr)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	sign(req, id, now, proto.SumHex(raw))
	return do(req)
}

func Unpublish(ctx context.Context, server string, id proto.Identity, appID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, server+"/api/v1/apps/"+appID, nil)
	if err != nil {
		return "", err
	}
	sign(req, id, time.Now().Unix(), proto.SumHex(nil))
	return do(req)
}

func do(req *http.Request) (string, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error string }
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return "", fmt.Errorf("server said %d: %s", resp.StatusCode, e.Error)
		}
		return "", fmt.Errorf("server said %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return strings.TrimSpace(string(body)), nil
}
