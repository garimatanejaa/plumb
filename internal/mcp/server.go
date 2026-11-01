// Package mcp serves checked notes to agents over the Model Context Protocol
// on stdio (newline-delimited JSON-RPC 2.0). Every tool is read-only, and
// every path goes through the same jail as the CLI.
package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"plumb/internal/check"
	"plumb/internal/note"
	"plumb/internal/render"
	"plumb/internal/suggest"
)

const (
	defaultProtocol = "2025-06-18"
	maxNoteSize     = 1 << 20
)

// Server is a plumb MCP server rooted at one repository.
type Server struct {
	c       *check.Checker
	version string
}

// New creates a server whose notes and evidence are read only from root.
func New(root, version string) (*Server, error) {
	c, err := check.New(root)
	if err != nil {
		return nil, err
	}
	return &Server{c: c, version: version}, nil
}

type request struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve reads requests from in and writes responses to out until in ends.
func (s *Server) Serve(in io.Reader, out io.Writer) error {
	r := bufio.NewReaderSize(in, 64*1024)
	enc := json.NewEncoder(out)
	for {
		line, err := r.ReadBytes('\n')
		if t := strings.TrimSpace(string(line)); t != "" {
			if resp := s.handleLine([]byte(t)); resp != nil {
				if werr := enc.Encode(resp); werr != nil {
					return werr
				}
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (s *Server) handleLine(b []byte) *response {
	var req request
	if err := json.Unmarshal(b, &req); err != nil {
		return &response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}}
	}
	isNotification := len(req.ID) == 0
	result, rerr := s.dispatch(req)
	if isNotification {
		return nil
	}
	resp := &response{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		resp.Error = rerr
	} else {
		resp.Result = result
	}
	return resp
}

func (s *Server) dispatch(req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion == "" {
			p.ProtocolVersion = defaultProtocol
		}
		return map[string]any{
			"protocolVersion": p.ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "plumb", "version": s.version},
			"instructions":    "Read project notes with read_fresh_notes: claims whose evidence no longer holds are withheld.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": toolDefs()}, nil
	case "tools/call":
		var p struct {
			Name      string `json:"name"`
			Arguments struct {
				File string `json:"file"`
				Mark bool   `json:"mark"`
			} `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		return s.callTool(p.Name, p.Arguments.File, p.Arguments.Mark)
	}
	if strings.HasPrefix(req.Method, "notifications/") {
		return nil, nil
	}
	return nil, &rpcError{-32601, "method not found: " + req.Method}
}

func toolDefs() []map[string]any {
	schema := func(extra map[string]any) map[string]any {
		props := map[string]any{
			"file": map[string]any{
				"type":        "string",
				"description": "Note path relative to the repository root. Defaults to CLAUDE.md, then AGENTS.md.",
			},
		}
		for k, v := range extra {
			props[k] = v
		}
		return map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	}
	ro := map[string]any{"readOnlyHint": true, "openWorldHint": false}
	return []map[string]any{
		{
			"name":        "check_notes",
			"description": "Verify every evidence-backed claim in a project note against the repository and report which are fresh and which are stale.",
			"inputSchema": schema(nil),
			"annotations": ro,
		},
		{
			"name":        "read_fresh_notes",
			"description": "Read a project note with stale claims removed (or marked with mark=true). The check runs on every call, so stale claims never reach you.",
			"inputSchema": schema(map[string]any{"mark": map[string]any{"type": "boolean", "description": "Mark stale claims with [STALE] instead of removing them."}}),
			"annotations": ro,
		},
		{
			"name":        "suggest_evidence",
			"description": "Propose plumb evidence comments for note bullets that have none, as a diff. Nothing is written.",
			"inputSchema": schema(nil),
			"annotations": ro,
		},
	}
}

func (s *Server) callTool(name, file string, mark bool) (any, *rpcError) {
	switch name {
	case "check_notes", "read_fresh_notes", "suggest_evidence":
	default:
		return nil, &rpcError{-32602, "unknown tool: " + name}
	}
	rel, src, err := s.readNote(file)
	if err != nil {
		return toolResult(err.Error(), true), nil
	}
	claims := note.Parse(src)
	switch name {
	case "check_notes":
		return toolResult(render.Text(rel, s.c.Verify(claims)), false), nil
	case "read_fresh_notes":
		rs := s.c.Verify(claims)
		out := render.Filter(src, rs, !mark)
		if _, stale := render.Counts(rs); stale > 0 {
			verb := "withheld"
			if mark {
				verb = "marked [STALE]"
			}
			out = strings.TrimRight(out, "\n") + fmt.Sprintf("\n\n<!-- plumb: %d stale claim(s) %s; their evidence no longer holds -->\n", stale, verb)
		}
		return toolResult(out, false), nil
	default:
		diff := suggest.Diff(rel, suggest.Suggest(s.c, src, rel))
		if diff == "" {
			diff = "no evidence could be proposed for the bullets without evidence"
		}
		return toolResult(diff, false), nil
	}
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

// readNote resolves a note through the path jail and reads it.
func (s *Server) readNote(file string) (rel, src string, err error) {
	cands := []string{file}
	if file == "" {
		cands = []string{"CLAUDE.md", "AGENTS.md"}
	}
	for _, f := range cands {
		abs, ok, err := s.c.Resolve(f)
		if err != nil {
			return "", "", err
		}
		if !ok {
			continue
		}
		st, err := os.Stat(abs)
		if err != nil {
			return "", "", err
		}
		if !st.Mode().IsRegular() {
			return "", "", fmt.Errorf("%s is not a regular file", f)
		}
		if st.Size() > maxNoteSize {
			return "", "", fmt.Errorf("%s is larger than %d bytes", f, maxNoteSize)
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			return "", "", err
		}
		return filepath.ToSlash(filepath.Clean(f)), string(b), nil
	}
	if file != "" {
		return "", "", fmt.Errorf("%s: no such note", file)
	}
	return "", "", errors.New("no note found: pass file, or add CLAUDE.md or AGENTS.md at the repository root")
}
