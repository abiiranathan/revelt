// This file contains general-purpose HTTP request/response helpers — JSON encode/decode,
// file upload handling, and file download/streaming — so that applications
// built on revelt do not need to pull in an external web framework for these
// everyday concerns.
package revelt

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultMaxJSONBodyBytes caps request bodies decoded via DecodeJSON when
	// the caller does not specify an explicit limit. 1 MiB is generous for
	// typical API payloads while preventing unbounded memory growth from a
	// malicious or misbehaving client.
	defaultMaxJSONBodyBytes = 1 << 20 // 1 MiB

	// defaultMaxMultipartMemory is the amount of request body multipart.Reader
	// is permitted to hold in memory before spilling file parts to temporary
	// files on disk. Mirrors the net/http default.
	defaultMaxMultipartMemory = 32 << 20 // 32 MiB

	// streamCopyBufferSize is the buffer size used when streaming file
	// downloads to the response writer. 32 KiB balances syscall overhead
	// against memory usage for concurrent downloads.
	streamCopyBufferSize = 32 * 1024

	// defaultUploadFilePerm is the permission mode used when persisting
	// uploaded files to disk via SaveUploadedFile.
	defaultUploadFilePerm = 0o644

	// defaultUploadDirPerm is the permission mode used when creating
	// destination directories for uploaded files.
	defaultUploadDirPerm = 0o755
)

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

// ErrBodyTooLarge is returned by DecodeJSON when the request body exceeds the
// configured maximum size.
var ErrBodyTooLarge = errors.New("revelt: request body too large")

// ErrEmptyBody is returned by DecodeJSON when the request body contains no
// data at all.
var ErrEmptyBody = errors.New("revelt: request body is empty")

// ErrNoMultipartFile is returned by SaveUploadedFile and related helpers when
// the requested form field does not contain a file part.
var ErrNoMultipartFile = errors.New("revelt: no file found for form field")

// MalformedJSONError wraps a JSON decoding failure with the byte offset at
// which it occurred, so callers can surface actionable error messages to API
// clients instead of a bare "invalid character" message.
type MalformedJSONError struct {
	// Offset is the byte position in the request body where decoding failed.
	Offset int64
	// Err is the underlying error returned by encoding/json.
	Err error
}

// Error implements the error interface.
func (e *MalformedJSONError) Error() string {
	return fmt.Sprintf("revelt: malformed JSON at byte offset %d: %v", e.Offset, e.Err)
}

// Unwrap allows errors.Is/errors.As to reach the underlying json error.
func (e *MalformedJSONError) Unwrap() error { return e.Err }

// ---------------------------------------------------------------------------
// JSON response helpers
// ---------------------------------------------------------------------------

// jsonEncode writes v to w as JSON. It exists so call sites do not need to
// import encoding/json directly. Errors are the caller's responsibility to
// handle.
func jsonEncode(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}

// JSON writes v to w as a JSON response with the given HTTP status code and
// the "application/json; charset=utf-8" Content-Type header. It is the
// primary response helper for HandlerFunc implementations.
//
// JSON returns any error encountered while marshalling or writing the
// response body. Because the status line and headers are written before the
// body, a non-nil return value indicates the client may have received a
// truncated or empty body even though the status code was already sent —
// callers should log such errors rather than attempt to write a second
// response.
func JSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := jsonEncode(w, v); err != nil {
		return fmt.Errorf("revelt.JSON: encoding response: %w", err)
	}
	return nil
}

// JSONOk is a convenience wrapper around JSON that always uses
// http.StatusOK (200).
func JSONOk(w http.ResponseWriter, v any) error {
	return JSON(w, http.StatusOK, v)
}

// JSONError writes a JSON error envelope of the form {"error": message} with
// the given HTTP status code. Use this from an ErrorHandlerFunc or directly
// from a HandlerFunc to produce API-friendly error responses instead of the
// plain-text output of http.Error.
func JSONError(w http.ResponseWriter, status int, message string) error {
	return JSON(w, status, map[string]string{"error": message})
}

// DecodeJSON reads and decodes a JSON request body into dst, which must be a
// non-nil pointer. The request body is capped at maxBytes; pass 0 to use
// defaultMaxJSONBodyBytes (1 MiB).
//
// DecodeJSON rejects bodies containing more than one JSON value (trailing
// garbage after a valid object/array is treated as an error), which guards
// against clients that accidentally concatenate payloads.
//
// Returns ErrEmptyBody if the body has no content, ErrBodyTooLarge if the
// body exceeds maxBytes, or a *MalformedJSONError wrapping the underlying
// decode failure with a byte offset for diagnostics.
func DecodeJSON(r *http.Request, dst any, maxBytes ...int64) error {
	if r.Body == nil {
		return ErrEmptyBody
	}

	var maxMemory int64 = defaultMaxJSONBodyBytes
	if len(maxBytes) > 0 && maxBytes[0] > 0 {
		maxMemory = maxBytes[0]
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxMemory+1))
	if err != nil {
		return fmt.Errorf("revelt.DecodeJSON: reading body: %w", err)
	}
	if len(body) == 0 {
		return ErrEmptyBody
	}
	if int64(len(body)) > maxMemory {
		return ErrBodyTooLarge
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(dst); err != nil {
		return &MalformedJSONError{Offset: dec.InputOffset(), Err: err}
	}

	// Reject trailing content after the first JSON value (e.g. two concatenated
	// objects) by attempting one more decode and expecting io.EOF.
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		if err == nil {
			return &MalformedJSONError{
				Offset: dec.InputOffset(),
				Err:    errors.New("unexpected additional JSON value after body"),
			}
		}
		return &MalformedJSONError{Offset: dec.InputOffset(), Err: err}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Query / form parameter helpers
// ---------------------------------------------------------------------------

type Integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// QueryInt is a generic helper that returns the integer value of the named query parameter, or
// fallback if the parameter is absent or does not parse as an integer. It
// supports any integer type (signed or unsigned) via Go 1.18+ generics.
func QueryInt[T Integer](r *http.Request, name string, fallback ...T) T {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		if len(fallback) > 0 {
			return fallback[0]
		}
		return 0
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		if len(fallback) > 0 {
			return fallback[0]
		}
		return 0
	}
	return T(v)
}

// QueryBool returns the boolean value of the named query parameter, or
// fallback if the parameter is absent or does not parse as a boolean.
// Accepts the same formats as strconv.ParseBool ("1", "t", "T", "TRUE",
// "true", "True", "0", "f", "F", "FALSE", "false", "False").
// At also supports "on" and "off" for convenience, which are not accepted by strconv.ParseBool
// because checkboxes in HTML forms submit "on" when checked and omit the parameter entirely when unchecked.
func QueryBool(r *http.Request, name string, fallback ...bool) bool {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		if len(fallback) > 0 {
			return fallback[0]
		}
		return false
	}

	v, err := strconv.ParseBool(raw)
	if err != nil {
		// strconv.ParseBool does not accept "on" or "off", so handle those explicitly.
		switch strings.ToLower(raw) {
		case "on":
			return true
		case "off":
			return false
		}

		if len(fallback) > 0 {
			return fallback[0]
		}
		return false
	}
	return v
}

// QueryDefault returns the named query parameter, or fallback if it is
// absent or empty.
func QueryDefault(r *http.Request, name, fallback string) string {
	if v := r.URL.Query().Get(name); v != "" {
		return v
	}
	return fallback
}

// ---------------------------------------------------------------------------
// File upload (multipart) helpers
// ---------------------------------------------------------------------------

// UploadedFile describes a single file received via a multipart form,
// bundling the parsed header with metadata convenient for validation.
type UploadedFile struct {
	// Header is the underlying multipart file header (filename, size, MIME
	// headers as declared by the client — not to be trusted for security
	// decisions without independent verification).
	Header *multipart.FileHeader

	// Filename is the client-supplied filename, sanitised to its base name
	// only (directory components stripped) to prevent path traversal when
	// callers use it to construct a destination path.
	Filename string

	// Ext is the lowercased file extension including the leading dot (e.g.
	// ".png"), derived from Filename. Empty if there is no extension.
	Ext string

	// Size is the file size in bytes as reported by the multipart header.
	Size int64
}

// ParseMultipartForm parses r's multipart form, spilling parts larger than
// maxMemory to temporary files on disk. Pass 0 for maxMemory to use
// defaultMaxMultipartMemory (32 MiB). Callers are responsible for calling
// r.MultipartForm.RemoveAll() (typically via defer) after the request has
// been fully handled, to clean up any temporary files created during
// parsing.
//
// ParseMultipartForm is idempotent: calling it multiple times on the same
// request after a successful first call is a cheap no-op courtesy of the
// standard library's internal caching on http.Request.
func ParseMultipartForm(r *http.Request, maxMemory int64) error {
	if maxMemory <= 0 {
		maxMemory = defaultMaxMultipartMemory
	}
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		return fmt.Errorf("revelt: parsing multipart form: %w", err)
	}
	return nil
}

// FormFile extracts metadata and an open handle for the file uploaded under
// the given form field name. The caller must close the returned
// multipart.File when done reading it.
//
// Returns ErrNoMultipartFile if the field is absent or contains no file.
func FormFile(r *http.Request, field string) (multipart.File, *UploadedFile, error) {
	f, header, err := r.FormFile(field)
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return nil, nil, ErrNoMultipartFile
		}
		return nil, nil, fmt.Errorf("revelt: reading form file %q: %w", field, err)
	}

	name := filepath.Base(filepath.Clean(header.Filename))
	// filepath.Base of a path ending in a separator, or of "." / "..", can
	// return values unsuitable as filenames; fall back to a safe default.
	if name == "." || name == ".." || name == string(filepath.Separator) {
		name = "upload"
	}

	uf := &UploadedFile{
		Header:   header,
		Filename: name,
		Ext:      strings.ToLower(filepath.Ext(name)),
		Size:     header.Size,
	}
	return f, uf, nil
}

// SaveUploadedFile copies the file uploaded under the given form field to
// destPath on disk, creating any missing parent directories along the way.
// It returns metadata about the file that was saved.
//
// SaveUploadedFile streams the upload directly to disk without buffering the
// full contents in memory, making it suitable for large files regardless of
// the maxMemory threshold passed to ParseMultipartForm.
//
// If maxBytes is greater than zero, the copy is aborted with ErrBodyTooLarge
// once that many bytes have been written, and the partially written
// destination file is removed. Pass 0 to allow files of any size (bounded
// only by the client-declared Content-Length and available disk space).
func SaveUploadedFile(r *http.Request, field, destPath string, maxBytes int64) (*UploadedFile, error) {
	src, meta, err := FormFile(r, field)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), defaultUploadDirPerm); err != nil {
		return nil, fmt.Errorf("revelt: creating destination directory: %w", err)
	}

	dst, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, defaultUploadFilePerm)
	if err != nil {
		return nil, fmt.Errorf("revelt: creating destination file: %w", err)
	}

	var reader io.Reader = src
	if maxBytes > 0 {
		// Read one byte beyond the limit so we can distinguish "exactly at
		// the limit" from "over the limit" without an off-by-one error.
		reader = io.LimitReader(src, maxBytes+1)
	}

	written, copyErr := io.Copy(dst, reader)
	closeErr := dst.Close()

	if maxBytes > 0 && written > maxBytes {
		_ = os.Remove(destPath)
		return nil, ErrBodyTooLarge
	}
	if copyErr != nil {
		_ = os.Remove(destPath)
		return nil, fmt.Errorf("revelt: writing uploaded file: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(destPath)
		return nil, fmt.Errorf("revelt: closing uploaded file: %w", closeErr)
	}

	meta.Size = written
	return meta, nil
}

// FormFiles extracts metadata and open handles for every file uploaded under
// the given form field name (for <input multiple> fields). The caller must
// close each returned multipart.File when done, and should do so even if a
// later element in the slice fails to open — inspect the returned error
// slice element for element i to know whether files[i] is valid to use.
//
// FormFiles requires ParseMultipartForm to have been called first (directly
// or via an earlier FormFile/SaveUploadedFile call on the same request,otherwise it's called
// with maxMemory of 0 which means no memory limit).
func FormFiles(r *http.Request, field string) ([]multipart.File, []*UploadedFile, error) {
	if r.MultipartForm == nil {
		if err := ParseMultipartForm(r, 0); err != nil {
			return nil, nil, err
		}
	}

	headers, ok := r.MultipartForm.File[field]
	if !ok || len(headers) == 0 {
		return nil, nil, ErrNoMultipartFile
	}

	files := make([]multipart.File, 0, len(headers))
	metas := make([]*UploadedFile, 0, len(headers))

	for _, h := range headers {
		f, err := h.Open()
		if err != nil {
			// Close everything opened so far before returning the error to
			// avoid leaking file descriptors.
			for _, opened := range files {
				_ = opened.Close()
			}
			return nil, nil, fmt.Errorf("revelt: opening uploaded file %q: %w", h.Filename, err)
		}

		name := filepath.Base(filepath.Clean(h.Filename))
		if name == "." || name == ".." || name == string(filepath.Separator) {
			name = "upload"
		}

		files = append(files, f)
		metas = append(metas, &UploadedFile{
			Header:   h,
			Filename: name,
			Ext:      strings.ToLower(filepath.Ext(name)),
			Size:     h.Size,
		})
	}

	return files, metas, nil
}

// ---------------------------------------------------------------------------
// File download / streaming helpers
// ---------------------------------------------------------------------------

// ServeFile streams the file at path to w as a download, setting
// Content-Type (sniffed from the file extension, falling back to
// "application/octet-stream"), Content-Length, and Content-Disposition
// headers. If downloadName is non-empty, the response is marked as an
// attachment with that suggested filename; otherwise the file is served
// inline using its base name.
//
// ServeFile supports HTTP range requests transparently via
// http.ServeContent, so it is suitable for serving media files that clients
// may seek within (e.g. video/audio players issuing Range headers).
func ServeFile(w http.ResponseWriter, r *http.Request, path string, downloadName string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("revelt.ServeFile: %w: %s", os.ErrNotExist, path)
		}
		return fmt.Errorf("revelt.ServeFile: opening %s: %w", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("revelt.ServeFile: stat %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("revelt.ServeFile: %s is a directory", path)
	}

	name := filepath.Base(path)
	disposition := "inline"
	if downloadName != "" {
		name = downloadName
		disposition = "attachment"
	}

	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`%s; filename="%s"`, disposition, sanitizeDispositionFilename(name)))

	// http.ServeContent handles Range, If-Modified-Since, ETag negotiation,
	// and Content-Length for us based on the file's ReadSeeker and ModTime.
	http.ServeContent(w, r, name, info.ModTime(), f)
	return nil
}

// StreamReader copies src to w as a download using a fixed-size buffer,
// without requiring src to implement io.Seeker (unlike ServeFile /
// http.ServeContent). Use this for on-the-fly generated content — e.g. a
// dynamically produced ZIP or CSV export — where there is no underlying
// os.File to seek within and range requests are not meaningful.
//
// If size is >= 0 it is written as the Content-Length header, letting
// clients show accurate progress; pass -1 if the size is not known ahead of
// time, in which case the response is sent using chunked transfer encoding.
// contentType and downloadName follow the same semantics as ServeFile;
// downloadName may be empty to serve inline with a generic name.
func StreamReader(w http.ResponseWriter, src io.Reader, contentType, downloadName string, size int64) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)

	disposition := "inline"
	name := downloadName
	if name == "" {
		name = "download"
	} else {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`%s; filename="%s"`, disposition, sanitizeDispositionFilename(name)))

	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}

	buf := make([]byte, streamCopyBufferSize)
	if _, err := io.CopyBuffer(w, src, buf); err != nil {
		return fmt.Errorf("revelt.StreamReader: copying response body: %w", err)
	}
	return nil
}

// sanitizeDispositionFilename strips characters that would break a naive
// Content-Disposition header (quotes, control characters, path separators)
// and percent-encodes the result as a fallback for non-ASCII names. This is
// a defensive measure, not a full RFC 6266 implementation — for filenames
// with extensive Unicode content, consider adding an explicit
// filename*=UTF-8”... parameter alongside the ASCII fallback this function
// produces.
func sanitizeDispositionFilename(name string) string {
	name = filepath.Base(name)
	replacer := strings.NewReplacer(
		`"`, "'",
		"\r", "",
		"\n", "",
		"\\", "_",
	)
	cleaned := replacer.Replace(name)
	if isASCIIPrintable(cleaned) {
		return cleaned
	}
	return url.QueryEscape(cleaned)
}

// isASCIIPrintable reports whether s consists solely of printable ASCII
// characters (0x20–0x7E), so callers can decide whether a raw filename is
// safe to embed directly in a header value.
func isASCIIPrintable(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c > 0x7E {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Chunked / progressive response helpers
// ---------------------------------------------------------------------------

// FlushWriter wraps an http.ResponseWriter that supports http.Flusher,
// flushing after every Write call. Use it to push partial output to the
// client immediately — e.g. for server-sent events or long-running exports
// that report progress — instead of letting it buffer until the handler
// returns.
//
// FlushWriter is not safe for concurrent use by multiple goroutines; callers
// must serialise their own writes.
type FlushWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

// NewFlushWriter wraps w for auto-flushing writes. If w does not implement
// http.Flusher (uncommon for standard net/http servers, but possible behind
// certain custom ResponseWriter wrappers), Write falls through to a plain
// write without flushing and ok is false so callers can decide whether to
// fall back to a different streaming strategy.
func NewFlushWriter(w http.ResponseWriter) (fw *FlushWriter, ok bool) {
	f, ok := w.(http.Flusher)
	return &FlushWriter{w: w, f: f}, ok
}

// Write implements io.Writer, flushing the underlying ResponseWriter after
// every successful write so data reaches the client without delay.
func (fw *FlushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if err != nil {
		return n, fmt.Errorf("revelt.FlushWriter: %w", err)
	}
	if fw.f != nil {
		fw.f.Flush()
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// Request body inspection helpers
// ---------------------------------------------------------------------------

// PeekContentType returns the request's declared media type (the
// Content-Type header with any parameters such as charset or boundary
// stripped). Returns an empty string if no Content-Type header is present or
// it cannot be parsed.
func PeekContentType(r *http.Request) string {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return ""
	}
	return mediaType
}

// IsJSONRequest reports whether the request declares a JSON Content-Type
// ("application/json" or any "+json" structured syntax suffix per RFC 6839,
// e.g. "application/merge-patch+json").
func IsJSONRequest(r *http.Request) bool {
	mediaType := PeekContentType(r)
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

// IsMultipartRequest reports whether the request declares a multipart/*
// Content-Type, i.e. it should be routed through ParseMultipartForm rather
// than DecodeJSON.
func IsMultipartRequest(r *http.Request) bool {
	return strings.HasPrefix(PeekContentType(r), "multipart/")
}

// ---------------------------------------------------------------------------
// Buffered response line reading (for streaming clients / debugging)
// ---------------------------------------------------------------------------

// ReadLines returns a bufio.Scanner over r's body configured with a
// reasonably large initial buffer, for handlers that need to process a
// request body line-by-line (e.g. newline-delimited JSON / NDJSON uploads)
// without loading the entire body into memory at once.
//
// The caller remains responsible for closing r.Body; ReadLines does not do
// so, since the scanner may still be in use when the handler returns control
// to a wrapping middleware.
func ReadLines(r *http.Request) *bufio.Scanner {
	scanner := bufio.NewScanner(r.Body)
	// Default bufio.Scanner token limit (64 KiB) is too small for some NDJSON
	// lines; start larger and let it grow up to 4 MiB per line.
	const initialBufSize = 64 * 1024
	const maxBufSize = 4 << 20
	scanner.Buffer(make([]byte, initialBufSize), maxBufSize)
	return scanner
}

// ---------------------------------------------------------------------------
// Misc response helpers
// ---------------------------------------------------------------------------

// NoContent writes an HTTP 204 No Content response with no body. Use this
// for successful mutations that have nothing meaningful to return (e.g.
// DELETE handlers).
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// Redirect writes an HTTP redirect to target using the given status code
// (typically http.StatusFound, http.StatusMovedPermanently, or
// http.StatusSeeOther). It is a thin wrapper over http.Redirect provided so
// handlers only need to import the revelt package for common response
// operations.
func Redirect(w http.ResponseWriter, r *http.Request, target string, status int) {
	http.Redirect(w, r, target, status)
}

// SetNoCache sets response headers instructing clients and intermediate
// caches not to store or reuse the response. Useful for API endpoints
// serving sensitive or highly dynamic data outside the static-asset pipeline
// already handled by registerStaticHandler.
func SetNoCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

// SetCacheImmutable sets response headers marking the response as safe to
// cache indefinitely (subject to maxAge seconds), for content-addressed or
// otherwise never-changing responses outside the built-in static handler's
// chunks/ convention.
func SetCacheImmutable(w http.ResponseWriter, maxAge time.Duration) {
	w.Header().Set("Cache-Control",
		fmt.Sprintf("public, max-age=%d, immutable", int(maxAge.Seconds())))
}
