package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type apiLogsHandler struct {
	db *pgxpool.Pool
}

func (h *apiLogsHandler) stream(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	deploymentID := r.PathValue("id")
	if deploymentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing deployment id"})
		return
	}

	if _, err := deploymentForUser(r.Context(), h.db, deploymentID, userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "deployment not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	lastOffset := 0
	lastStatus := ""
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		default:
		}

		var buildLog, status string
		err := h.db.QueryRow(r.Context(), `
			SELECT build_log, status
			FROM deployments
			WHERE id = $1
		`, deploymentID).Scan(&buildLog, &status)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				_ = writeSSE(w, map[string]string{"type": "error", "error": "query failed"})
				flusher.Flush()
			}
			return
		}

		lines := splitLogLines(buildLog)
		if lastOffset < len(lines) {
			for _, line := range lines[lastOffset:] {
				if err := writeSSE(w, map[string]string{"type": "log", "line": line}); err != nil {
					return
				}
			}
			lastOffset = len(lines)
			flusher.Flush()
		}

		if status != lastStatus {
			if err := writeSSE(w, map[string]string{"type": "status", "status": status}); err != nil {
				return
			}
			flusher.Flush()
			lastStatus = status
		}

		if isTerminalDeploymentStatus(status) {
			_ = writeSSE(w, map[string]string{"type": "done", "status": status})
			flusher.Flush()
			return
		}

		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func splitLogLines(logOutput string) []string {
	if logOutput == "" {
		return nil
	}
	lines := strings.Split(logOutput, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func isTerminalDeploymentStatus(status string) bool {
	switch status {
	case "live", "failed", "cancelled", "stopped":
		return true
	default:
		return false
	}
}

func writeSSE(w http.ResponseWriter, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}
