package server

import (
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/config"
)

// logFileName is the server log written under config.Dir().
const logFileName = "server.log"

// setupLogging builds the process logger. Output always goes to the console
// (stdout) and, when the config dir is writable, is mirrored to
// ~/.config/fusionlocalserver/server.log. Default level is info — essential
// lines only (startup, warnings, errors, auth events). Verbose (-v) raises the
// level to debug, which adds the per-request line (see middleware.logRequest)
// and routes the api package's GraphQL request/response tracing to the same
// sinks (signed URLs redacted at the source).
//
// The returned closer closes the log file and is always safe to call.
func setupLogging(verbose bool) (*slog.Logger, func()) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}

	sinks := []io.Writer{os.Stdout}
	closer := func() {}
	f, path, ferr := openLogFile()
	if ferr == nil {
		// Rotate at 10 MB, keep 3 compressed generations: server.log had
		// grown unbounded (23 MB observed) with no cap. An existing
		// oversized file rotates on its first post-upgrade size trip.
		lj := &lumberjack.Logger{
			Filename:   path,
			MaxSize:    10, // MB
			MaxBackups: 3,
			Compress:   true,
		}
		_ = f.Close() // lumberjack owns the handle from here
		sinks = append(sinks, lj)
		closer = func() { _ = lj.Close() }
	}
	out := io.MultiWriter(sinks...)

	logger := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: level}))
	if ferr != nil {
		logger.Warn("could not open log file; logging to console only", "err", ferr)
	} else {
		logger.Info("logging", "file", path, "verbose", verbose)
	}

	if verbose {
		api.EnableDebug(out)
	}
	return logger, closer
}

// openLogFile opens (creating/appending) the server log under config.Dir().
func openLogFile() (*os.File, string, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, logFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, "", err
	}
	return f, path, nil
}

// httpErrorLog adapts http.Server.ErrorLog onto slog, so the server's
// internal complaints share the process sinks instead of leaking to stderr —
// and so the noisiest of them can be demoted. Bare connections that open and
// close without a TLS handshake (port-liveness probes, browsers'
// speculative preconnects, scanners) each log "http: TLS handshake error
// ... EOF"; at one line per probe they flood the log while signalling
// nothing, so that class drops to debug. Real handshake failures (bad
// certs, protocol mismatches) and everything else stay at error.
func httpErrorLog(logger *slog.Logger) *log.Logger {
	return log.New(httpErrLogWriter{logger: logger}, "", 0)
}

type httpErrLogWriter struct{ logger *slog.Logger }

func (w httpErrLogWriter) Write(p []byte) (int, error) {
	msg := strings.TrimSpace(string(p))
	if isHandshakeNoise(msg) {
		w.logger.Debug(msg)
	} else {
		w.logger.Error(msg)
	}
	return len(p), nil
}

// isHandshakeNoise reports whether an http.Server error line is a
// no-handshake disconnect rather than a genuine TLS failure.
func isHandshakeNoise(msg string) bool {
	if !strings.Contains(msg, "TLS handshake error") {
		return false
	}
	return strings.HasSuffix(msg, ": EOF") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "i/o timeout")
}
