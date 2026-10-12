package tools

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/inailong/ssh-mcp/internal/config"
	"github.com/inailong/ssh-mcp/internal/mcp"
	"github.com/inailong/ssh-mcp/internal/sshpool"
	"github.com/pkg/sftp"
)

const maxTransferFiles = 2000

func RegisterSFTP(server *mcp.Server, pool *sshpool.Pool, cfg *config.Config) {
	server.RegisterTool(mcp.Tool{Name: "read-file", Description: "Read a remote text file", InputSchema: schema([]string{"filePath"}, map[string]any{"filePath": map[string]any{"type": "string"}, "offset": map[string]any{"type": "number", "default": 0}, "length": map[string]any{"type": "number", "default": 65536, "maximum": 65536}, "connectionName": map[string]any{"type": "string"}}), Handler: func(ctx context.Context, a map[string]any) (*mcp.CallToolResult, error) {
		return readFile(ctx, pool, cfg, a)
	}})
	server.RegisterTool(mcp.Tool{Name: "write-file", Description: "Write a remote file", InputSchema: schema([]string{"filePath", "content"}, map[string]any{"filePath": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}, "append": map[string]any{"type": "boolean"}, "mode": map[string]any{"type": "number"}, "connectionName": map[string]any{"type": "string"}}), Handler: func(ctx context.Context, a map[string]any) (*mcp.CallToolResult, error) {
		return writeFile(ctx, pool, cfg, a)
	}})
	server.RegisterTool(mcp.Tool{Name: "upload", Description: "Upload files or directories", InputSchema: schema([]string{"localPath", "remotePath"}, map[string]any{"localPath": map[string]any{"type": "string"}, "remotePath": map[string]any{"type": "string"}, "overwrite": map[string]any{"type": "boolean", "default": true}, "connectionName": map[string]any{"type": "string"}}), Handler: func(ctx context.Context, a map[string]any) (*mcp.CallToolResult, error) {
		return upload(ctx, pool, cfg, a)
	}})
	server.RegisterTool(mcp.Tool{Name: "download", Description: "Download files or directories", InputSchema: schema([]string{"remotePath", "localPath"}, map[string]any{"remotePath": map[string]any{"type": "string"}, "localPath": map[string]any{"type": "string"}, "overwrite": map[string]any{"type": "boolean", "default": true}, "connectionName": map[string]any{"type": "string"}}), Handler: func(ctx context.Context, a map[string]any) (*mcp.CallToolResult, error) {
		return download(ctx, pool, cfg, a)
	}})
}
func schema(required []string, props map[string]any) map[string]any {
	return map[string]any{"type": "object", "required": required, "properties": props}
}
func numberArg(a map[string]any, k string, d int64) int64 {
	switch v := a[k].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	}
	return d
}
func boolArg(a map[string]any, k string, d bool) bool {
	if v, ok := a[k].(bool); ok {
		return v
	}
	return d
}
func sftpClient(ctx context.Context, pool *sshpool.Pool, a map[string]any) (*sftp.Client, error) {
	name, _ := a["connectionName"].(string)
	target, e := pool.ResolveTarget(name)
	if e != nil {
		return nil, e
	}
	c, e := pool.GetClient(ctx, target)
	if e != nil {
		return nil, e
	}
	return sftp.NewClient(c)
}
func allowed(path string, rules []string) bool {
	if len(rules) == 0 {
		return true
	}
	p, e := filepath.Abs(path)
	if e != nil {
		return false
	}
	for _, r := range rules {
		q, e := filepath.Abs(os.ExpandEnv(r))
		if e == nil && (p == q || strings.HasPrefix(p, q+string(filepath.Separator))) {
			return true
		}
	}
	return false
}
func remoteAllowed(path string, rules []string) bool {
	if len(rules) == 0 {
		return true
	}
	p := filepath.Clean(path)
	for _, r := range rules {
		q := filepath.Clean(r)
		if p == q || strings.HasPrefix(p, q+"/") {
			return true
		}
	}
	return false
}
func readFile(ctx context.Context, pool *sshpool.Pool, cfg *config.Config, a map[string]any) (*mcp.CallToolResult, error) {
	path, ok := a["filePath"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("filePath is required")
	}
	if cfg != nil && !remoteAllowed(path, cfg.AllowedRemotePaths) {
		return nil, fmt.Errorf("remote path denied")
	}
	off := numberArg(a, "offset", 0)
	if off < 0 {
		return nil, fmt.Errorf("offset must be nonnegative")
	}
	n := numberArg(a, "length", 65536)
	if n < 0 || n > 65536 {
		return nil, fmt.Errorf("length must be between 0 and 65536")
	}
	s, e := sftpClient(ctx, pool, a)
	if e != nil {
		return nil, e
	}
	defer s.Close()
	st, e := s.Stat(path)
	if e != nil {
		return nil, e
	}
	f, e := s.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	if off > st.Size() {
		off = st.Size()
	}
	b := make([]byte, n)
	got, e := f.ReadAt(b, off)
	if e != nil && e != io.EOF {
		return nil, e
	}
	b = b[:got]
	if strings.IndexByte(string(b), 0) >= 0 {
		return nil, fmt.Errorf("binary file rejected")
	}
	end := off + int64(got)
	h := fmt.Sprintf("bytes %d-%d of %d", off, end, st.Size())
	if end < st.Size() {
		h += fmt.Sprintf(" (next offset %d)", end)
	}
	return mcp.TextResult(h + "\n\n" + string(b)), nil
}
func writeFile(ctx context.Context, pool *sshpool.Pool, cfg *config.Config, a map[string]any) (*mcp.CallToolResult, error) {
	path, ok := a["filePath"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("filePath is required")
	}
	if cfg != nil && !remoteAllowed(path, cfg.AllowedRemotePaths) {
		return nil, fmt.Errorf("remote path denied")
	}
	content, ok := a["content"].(string)
	if !ok {
		return nil, fmt.Errorf("content is required")
	}
	s, e := sftpClient(ctx, pool, a)
	if e != nil {
		return nil, e
	}
	defer s.Close()
	if e = s.MkdirAll(filepath.Dir(path)); e != nil {
		return nil, e
	}
	flag := os.O_WRONLY | os.O_CREATE
	if boolArg(a, "append", false) {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	f, e := s.OpenFile(path, flag)
	if e != nil {
		return nil, e
	}
	n, e := f.Write([]byte(content))
	if e == nil {
		e = f.Close()
	} else {
		_ = f.Close()
	}
	if e != nil {
		return nil, e
	}
	if v, ok := a["mode"].(float64); ok {
		e = s.Chmod(path, os.FileMode(v))
	}
	if e != nil {
		return nil, e
	}
	return mcp.TextResult(fmt.Sprintf("wrote %d bytes to %s", n, path)), nil
}
func localFiles(root string) ([]string, error) {
	st, e := os.Lstat(root)
	if e != nil {
		return nil, e
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return nil, nil
	}
	if !st.IsDir() {
		return []string{root}, nil
	}
	var out []string
	e = filepath.Walk(root, func(p string, i os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if i.Mode()&os.ModeSymlink != 0 {
			if i.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !i.IsDir() {
			out = append(out, p)
			if len(out) > maxTransferFiles {
				return fmt.Errorf("too many files")
			}
		}
		return nil
	})
	return out, e
}
func upload(ctx context.Context, pool *sshpool.Pool, cfg *config.Config, a map[string]any) (*mcp.CallToolResult, error) {
	local, ok := a["localPath"].(string)
	if !ok {
		return nil, fmt.Errorf("localPath is required")
	}
	remote, ok := a["remotePath"].(string)
	if !ok {
		return nil, fmt.Errorf("remotePath is required")
	}
	if cfg != nil && !allowed(local, cfg.AllowedLocalPaths) {
		return nil, fmt.Errorf("local path denied")
	}
	if cfg != nil && !remoteAllowed(remote, cfg.AllowedRemotePaths) {
		return nil, fmt.Errorf("remote path denied")
	}
	files, e := localFiles(local)
	if e != nil {
		return nil, e
	}
	s, e := sftpClient(ctx, pool, a)
	if e != nil {
		return nil, e
	}
	defer s.Close()
	rootInfo, _ := os.Stat(local)
	var total int64
	for _, p := range files {
		rel := ""
		if rootInfo.IsDir() {
			rel, _ = filepath.Rel(local, p)
		}
		dst := remote
		if rel != "" {
			dst = filepath.Join(remote, filepath.ToSlash(rel))
		}
		if !boolArg(a, "overwrite", true) {
			if _, e = s.Stat(dst); e == nil {
				continue
			}
		}
		if e = s.MkdirAll(filepath.Dir(dst)); e != nil {
			return nil, e
		}
		in, e := os.Open(p)
		if e != nil {
			return nil, e
		}
		out, e := s.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC)
		if e == nil {
			var n int64
			n, e = io.Copy(out, in)
			total += n
		}
		_ = in.Close()
		_ = out.Close()
		if e != nil {
			return nil, e
		}
	}
	return mcp.TextResult(fmt.Sprintf("uploaded %d files, %d bytes", len(files), total)), nil
}
func download(ctx context.Context, pool *sshpool.Pool, cfg *config.Config, a map[string]any) (*mcp.CallToolResult, error) {
	remote, ok := a["remotePath"].(string)
	if !ok {
		return nil, fmt.Errorf("remotePath is required")
	}
	local, ok := a["localPath"].(string)
	if !ok {
		return nil, fmt.Errorf("localPath is required")
	}
	if cfg != nil && !remoteAllowed(remote, cfg.AllowedRemotePaths) {
		return nil, fmt.Errorf("remote path denied")
	}
	if cfg != nil && !allowed(local, cfg.AllowedLocalPaths) {
		return nil, fmt.Errorf("local path denied")
	}
	s, e := sftpClient(ctx, pool, a)
	if e != nil {
		return nil, e
	}
	defer s.Close()
	info, e := s.Stat(remote)
	if e != nil {
		return nil, e
	}
	type pair struct{ r, l string }
	items := []pair{}
	if !info.IsDir() {
		items = []pair{{remote, local}}
	} else {
		w := []string{remote}
		for len(w) > 0 {
			d := w[0]
			w = w[1:]
			es, e := s.ReadDir(d)
			if e != nil {
				return nil, e
			}
			for _, x := range es {
				r := filepath.Join(d, x.Name())
				l := filepath.Join(local, strings.TrimPrefix(r, remote+string(filepath.Separator)))
				if x.Mode()&os.ModeSymlink != 0 {
					continue
				}
				if x.IsDir() {
					w = append(w, r)
				} else {
					items = append(items, pair{r, l})
					if len(items) > maxTransferFiles {
						return nil, fmt.Errorf("too many files")
					}
				}
			}
		}
	}
	var total int64
	for _, x := range items {
		if !boolArg(a, "overwrite", true) {
			if _, e = os.Stat(x.l); e == nil {
				continue
			}
		}
		if e = os.MkdirAll(filepath.Dir(x.l), 0755); e != nil {
			return nil, e
		}
		in, e := s.Open(x.r)
		if e != nil {
			return nil, e
		}
		out, e := os.OpenFile(x.l, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if e == nil {
			var n int64
			n, e = io.Copy(out, in)
			total += n
		}
		_ = in.Close()
		_ = out.Close()
		if e != nil {
			return nil, e
		}
	}
	return mcp.TextResult(fmt.Sprintf("downloaded %d files, %d bytes", len(items), total)), nil
}

var _ = sort.Strings
