package revelt

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// JSON response helpers
// ---------------------------------------------------------------------------

// TestJSON verifies that JSON writes the correct status code, Content-Type
// header, and marshalled body.
func TestJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	payload := map[string]any{"name": "revelt", "count": 3}

	if err := JSON(rec, http.StatusCreated, payload); err != nil {
		t.Fatalf("JSON() returned unexpected error: %v", err)
	}

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json; charset=utf-8")
	}

	body := strings.TrimSpace(rec.Body.String())
	if !strings.Contains(body, `"name":"revelt"`) || !strings.Contains(body, `"count":3`) {
		t.Errorf("body = %q, missing expected fields", body)
	}
}

// TestJSONOk verifies JSONOk always uses HTTP 200.
func TestJSONOk(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := JSONOk(rec, map[string]bool{"ok": true}); err != nil {
		t.Fatalf("JSONOk() returned unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// TestJSONError verifies the error envelope shape and status code.
func TestJSONError(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := JSONError(rec, http.StatusBadRequest, "invalid input"); err != nil {
		t.Fatalf("JSONError() returned unexpected error: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	want := `{"error":"invalid input"}` + "\n"
	if rec.Body.String() != want {
		t.Errorf("body = %q, want %q", rec.Body.String(), want)
	}
}

// TestDecodeJSON_Success verifies a well-formed single JSON object decodes
// correctly.
func TestDecodeJSON_Success(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"nathan"}`))
	var got payload
	if err := DecodeJSON(req, &got, 0); err != nil {
		t.Fatalf("DecodeJSON() returned unexpected error: %v", err)
	}
	if got.Name != "nathan" {
		t.Errorf("Name = %q, want %q", got.Name, "nathan")
	}
}

// TestDecodeJSON_EmptyBody verifies an empty body yields ErrEmptyBody.
func TestDecodeJSON_EmptyBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	var got map[string]any
	err := DecodeJSON(req, &got)
	if !errors.Is(err, ErrEmptyBody) {
		t.Errorf("err = %v, want ErrEmptyBody", err)
	}
}

// TestDecodeJSON_TooLarge verifies bodies exceeding maxBytes yield
// ErrBodyTooLarge rather than a generic decode error.
func TestDecodeJSON_TooLarge(t *testing.T) {
	body := `{"data":"` + strings.Repeat("x", 100) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))

	var got map[string]any
	err := DecodeJSON(req, &got, 10) // way smaller than the body
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Errorf("err = %v, want ErrBodyTooLarge", err)
	}
}

// TestDecodeJSON_Malformed verifies syntactically invalid JSON is wrapped in
// a *MalformedJSONError with a usable offset, reachable via errors.As.
func TestDecodeJSON_Malformed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":`))
	var got map[string]any

	err := DecodeJSON(req, &got, 0)
	var malformed *MalformedJSONError
	if !errors.As(err, &malformed) {
		t.Fatalf("err = %v, want *MalformedJSONError", err)
	}
	if malformed.Offset != 0 {
		t.Errorf("Offset = %d, want 0", malformed.Offset)
	}
}

// TestDecodeJSON_TrailingGarbage verifies that a second concatenated JSON
// value after a valid one is rejected instead of silently ignored.
func TestDecodeJSON_TrailingGarbage(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":1}{"b":2}`))
	var got map[string]any

	err := DecodeJSON(req, &got, 0)
	if err == nil {
		t.Fatal("DecodeJSON() returned nil error, want error for trailing garbage")
	}
	var malformed *MalformedJSONError
	if !errors.As(err, &malformed) {
		t.Errorf("err = %v, want *MalformedJSONError", err)
	}
}

// ---------------------------------------------------------------------------
// Query parameter helpers
// ---------------------------------------------------------------------------

// TestQueryInt covers present/valid, present/invalid, and absent cases.
func TestQueryInt(t *testing.T) {
	tests := []struct {
		name     string
		rawQuery string
		key      string
		fallback int
		want     int
	}{
		{"valid int", "page=5", "page", 1, 5},
		{"invalid int falls back", "page=abc", "page", 1, 1},
		{"absent key falls back", "", "page", 7, 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?"+tt.rawQuery, nil)
			if got := QueryInt(req, tt.key, tt.fallback); got != tt.want {
				t.Errorf("QueryInt() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestQueryBool covers true/false/invalid/absent cases.
func TestQueryBool(t *testing.T) {
	tests := []struct {
		name     string
		rawQuery string
		fallback bool
		want     bool
	}{
		{"true", "active=true", false, true},
		{"false", "active=false", true, false},
		{"invalid falls back", "active=maybe", true, true},
		{"absent falls back", "", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?"+tt.rawQuery, nil)
			if got := QueryBool(req, "active", tt.fallback); got != tt.want {
				t.Errorf("QueryBool() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestQueryDefault verifies fallback is only used when the parameter is
// absent or empty.
func TestQueryDefault(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?sort=&name=nathan", nil)

	if got := QueryDefault(req, "name", "default"); got != "nathan" {
		t.Errorf("QueryDefault(name) = %q, want %q", got, "nathan")
	}
	if got := QueryDefault(req, "sort", "asc"); got != "asc" {
		t.Errorf("QueryDefault(sort) = %q, want %q (empty value should fall back)", got, "asc")
	}
	if got := QueryDefault(req, "missing", "fallback"); got != "fallback" {
		t.Errorf("QueryDefault(missing) = %q, want %q", got, "fallback")
	}
}

// ---------------------------------------------------------------------------
// Multipart upload helpers
// ---------------------------------------------------------------------------

// newMultipartRequest builds an *http.Request carrying a single-file
// multipart form under fieldName with the given filename and content.
func newMultipartRequest(t *testing.T, fieldName, filename string, content []byte) *http.Request {
	t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	part, err := mw.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("CreateFormFile() error: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("writing part content: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("closing multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// TestFormFile_Success verifies metadata extraction and path-traversal
// sanitisation of the client-supplied filename.
func TestFormFile_Success(t *testing.T) {
	content := []byte("hello world")
	req := newMultipartRequest(t, "file", "../../etc/passwd.txt", content)

	f, meta, err := FormFile(req, "file")
	if err != nil {
		t.Fatalf("FormFile() returned unexpected error: %v", err)
	}
	defer f.Close()

	if meta.Filename != "passwd.txt" {
		t.Errorf("Filename = %q, want %q (path components stripped)", meta.Filename, "passwd.txt")
	}
	if meta.Ext != ".txt" {
		t.Errorf("Ext = %q, want %q", meta.Ext, ".txt")
	}
	if meta.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", meta.Size, len(content))
	}
}

// TestFormFile_MissingField verifies ErrNoMultipartFile is returned when the
// requested field is absent from the form.
func TestFormFile_MissingField(t *testing.T) {
	req := newMultipartRequest(t, "file", "a.txt", []byte("x"))

	_, _, err := FormFile(req, "does-not-exist")
	if !errors.Is(err, ErrNoMultipartFile) {
		t.Errorf("err = %v, want ErrNoMultipartFile", err)
	}
}

// TestSaveUploadedFile_Success verifies the uploaded content is written
// byte-for-byte to destPath and metadata reflects the written size.
func TestSaveUploadedFile_Success(t *testing.T) {
	content := []byte("the quick brown fox jumps over the lazy dog")
	req := newMultipartRequest(t, "file", "fox.txt", content)

	destDir := t.TempDir()
	destPath := filepath.Join(destDir, "nested", "saved.txt")

	meta, err := SaveUploadedFile(req, "file", destPath, 0)
	if err != nil {
		t.Fatalf("SaveUploadedFile() returned unexpected error: %v", err)
	}
	if meta.Size != int64(len(content)) {
		t.Errorf("meta.Size = %d, want %d", meta.Size, len(content))
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("reading saved file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("saved content = %q, want %q", got, content)
	}
}

// TestSaveUploadedFile_TooLarge verifies that exceeding maxBytes aborts the
// write, returns ErrBodyTooLarge, and removes the partially written file.
func TestSaveUploadedFile_TooLarge(t *testing.T) {
	content := bytes.Repeat([]byte("a"), 1000)
	req := newMultipartRequest(t, "file", "big.bin", content)

	destPath := filepath.Join(t.TempDir(), "big.bin")

	_, err := SaveUploadedFile(req, "file", destPath, 100)
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("err = %v, want ErrBodyTooLarge", err)
	}
	if _, statErr := os.Stat(destPath); !os.IsNotExist(statErr) {
		t.Errorf("expected destPath to be removed after size-limit abort, stat err = %v", statErr)
	}
}

// TestFormFiles_Multiple verifies multi-file extraction under a repeated
// field name, and that filenames are sanitised individually.
func TestFormFiles_Multiple(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	names := []string{"one.txt", "../two.txt"}
	for i, name := range names {
		part, err := mw.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("CreateFormFile() error: %v", err)
		}
		if _, err := part.Write([]byte{byte('a' + i)}); err != nil {
			t.Fatalf("writing part: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("closing multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	files, metas, err := FormFiles(req, "files")
	if err != nil {
		t.Fatalf("FormFiles() returned unexpected error: %v", err)
	}
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()

	if len(files) != 2 || len(metas) != 2 {
		t.Fatalf("got %d files / %d metas, want 2 / 2", len(files), len(metas))
	}
	if metas[1].Filename != "two.txt" {
		t.Errorf("metas[1].Filename = %q, want %q (path stripped)", metas[1].Filename, "two.txt")
	}
}

// ---------------------------------------------------------------------------
// File download / streaming helpers
// ---------------------------------------------------------------------------

// TestServeFile_Success verifies status, headers, and body for a normal file
// download.
func TestServeFile_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.txt")
	content := []byte("report contents")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("writing fixture file: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/download", nil)
	rec := httptest.NewRecorder()

	if err := ServeFile(rec, req, path, "custom-name.txt"); err != nil {
		t.Fatalf("ServeFile() returned unexpected error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !bytes.Equal(rec.Body.Bytes(), content) {
		t.Errorf("body = %q, want %q", rec.Body.Bytes(), content)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, `attachment`) || !strings.Contains(cd, `custom-name.txt`) {
		t.Errorf("Content-Disposition = %q, want attachment with custom-name.txt", cd)
	}
}

// TestServeFile_NotFound verifies a missing file surfaces os.ErrNotExist via
// errors.Is, so callers can map it to a 404.
func TestServeFile_NotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/download", nil)
	rec := httptest.NewRecorder()

	err := ServeFile(rec, req, filepath.Join(t.TempDir(), "missing.txt"), "")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want wrapped os.ErrNotExist", err)
	}
}

// TestServeFile_RejectsDirectory verifies directories are rejected rather
// than silently served.
func TestServeFile_RejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	req := httptest.NewRequest(http.MethodGet, "/download", nil)
	rec := httptest.NewRecorder()

	if err := ServeFile(rec, req, dir, ""); err == nil {
		t.Error("ServeFile() returned nil error for a directory, want error")
	}
}

// TestStreamReader_KnownSize verifies Content-Length is set and the body is
// copied through completely when size is known.
func TestStreamReader_KnownSize(t *testing.T) {
	content := []byte("streamed content")
	rec := httptest.NewRecorder()

	err := StreamReader(rec, bytes.NewReader(content), "text/csv", "export.csv", int64(len(content)))
	if err != nil {
		t.Fatalf("StreamReader() returned unexpected error: %v", err)
	}

	if got := rec.Header().Get("Content-Length"); got != "16" {
		t.Errorf("Content-Length = %q, want %q", got, "16")
	}
	if got := rec.Header().Get("Content-Type"); got != "text/csv" {
		t.Errorf("Content-Type = %q, want %q", got, "text/csv")
	}
	if !bytes.Equal(rec.Body.Bytes(), content) {
		t.Errorf("body = %q, want %q", rec.Body.Bytes(), content)
	}
}

// TestStreamReader_UnknownSize verifies no Content-Length header is set when
// size is negative, allowing chunked transfer encoding to take over.
func TestStreamReader_UnknownSize(t *testing.T) {
	rec := httptest.NewRecorder()
	err := StreamReader(rec, strings.NewReader("chunked"), "", "", -1)
	if err != nil {
		t.Fatalf("StreamReader() returned unexpected error: %v", err)
	}
	if got := rec.Header().Get("Content-Length"); got != "" {
		t.Errorf("Content-Length = %q, want empty for unknown size", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want default octet-stream", got)
	}
}

// TestSanitizeDispositionFilename verifies dangerous characters are stripped
// and non-ASCII names are percent-encoded rather than passed through raw.
func TestSanitizeDispositionFilename(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"strips quotes", `report"2024".txt`, `report'2024'.txt`},
		{"strips path", `../../secret.txt`, `secret.txt`},
		{"ascii passthrough", "plain.txt", "plain.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeDispositionFilename(tt.in); got != tt.want {
				t.Errorf("sanitizeDispositionFilename(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}

	// Non-ASCII input must not appear verbatim in the output.
	nonASCII := "résumé.pdf"
	got := sanitizeDispositionFilename(nonASCII)
	if got == nonASCII {
		t.Errorf("sanitizeDispositionFilename(%q) returned input unchanged, want percent-encoded", nonASCII)
	}
}

// ---------------------------------------------------------------------------
// Content-Type inspection helpers
// ---------------------------------------------------------------------------

// TestPeekContentType verifies parameters (e.g. charset) are stripped and
// absent/malformed headers yield an empty string.
func TestPeekContentType(t *testing.T) {
	tests := []struct {
		name string
		ct   string
		want string
	}{
		{"with charset", "application/json; charset=utf-8", "application/json"},
		{"no header", "", ""},
		{"malformed", ";;;", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			if tt.ct != "" {
				req.Header.Set("Content-Type", tt.ct)
			}
			if got := PeekContentType(req); got != tt.want {
				t.Errorf("PeekContentType() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestIsJSONRequest covers the plain and structured-syntax-suffix (+json)
// cases per RFC 6839.
func TestIsJSONRequest(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"application/json", true},
		{"application/merge-patch+json", true},
		{"text/plain", false},
		{"", false},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		if tt.ct != "" {
			req.Header.Set("Content-Type", tt.ct)
		}
		if got := IsJSONRequest(req); got != tt.want {
			t.Errorf("IsJSONRequest(%q) = %v, want %v", tt.ct, got, tt.want)
		}
	}
}

// TestIsMultipartRequest verifies detection of multipart/* content types.
func TestIsMultipartRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xyz")
	if !IsMultipartRequest(req) {
		t.Error("IsMultipartRequest() = false, want true for multipart/form-data")
	}

	req2 := httptest.NewRequest(http.MethodPost, "/", nil)
	req2.Header.Set("Content-Type", "application/json")
	if IsMultipartRequest(req2) {
		t.Error("IsMultipartRequest() = true, want false for application/json")
	}
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

// TestNoContent verifies a bare 204 status with no body.
func TestNoContent(t *testing.T) {
	rec := httptest.NewRecorder()
	NoContent(rec)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body length = %d, want 0", rec.Body.Len())
	}
}

// TestSetNoCache verifies all three cache-busting headers are set.
func TestSetNoCache(t *testing.T) {
	rec := httptest.NewRecorder()
	SetNoCache(rec)

	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Errorf("Cache-Control = %q, want it to contain %q", got, "no-store")
	}
	if got := rec.Header().Get("Pragma"); got != "no-cache" {
		t.Errorf("Pragma = %q, want %q", got, "no-cache")
	}
}

// TestSetCacheImmutable verifies max-age is computed correctly from the
// supplied duration and the immutable directive is present.
func TestSetCacheImmutable(t *testing.T) {
	rec := httptest.NewRecorder()
	SetCacheImmutable(rec, 24*time.Hour)

	got := rec.Header().Get("Cache-Control")
	want := "public, max-age=86400, immutable"
	if got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// ReadLines
// ---------------------------------------------------------------------------

// TestReadLines verifies NDJSON-style line-by-line scanning over a request
// body, including a line exceeding the default bufio.Scanner token size to
// confirm the enlarged buffer is actually in effect.
func TestReadLines(t *testing.T) {
	lines := []string{
		`{"id":1}`,
		strings.Repeat("x", 70*1024), // exceeds bufio's default 64 KiB token limit
		`{"id":3}`,
	}
	body := strings.Join(lines, "\n")

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	scanner := ReadLines(req)

	var got []string
	for scanner.Scan() {
		got = append(got, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if len(got) != len(lines) {
		t.Fatalf("got %d lines, want %d", len(got), len(lines))
	}
	for i := range lines {
		if got[i] != lines[i] {
			t.Errorf("line %d length = %d, want %d", i, len(got[i]), len(lines[i]))
		}
	}
}
