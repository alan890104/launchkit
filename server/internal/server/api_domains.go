package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	domainsvc "github.com/alan890104/launchkit/server/internal/service/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type apiDomainsHandler struct {
	db      *pgxpool.Pool
	service *domainsvc.Service
}

type purchasedDomainResponse struct {
	ID            string    `json:"id"`
	Domain        string    `json:"domain"`
	Status        string    `json:"status"`
	ExpiresAt     time.Time `json:"expires_at"`
	DNSConfigured bool      `json:"dns_configured"`
	PurchasePrice float64   `json:"purchase_price"`
}

func (h *apiDomainsHandler) list(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	projectID := r.PathValue("id")
	if projectID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project id"})
		return
	}

	teamID, err := projectTeamForUser(r.Context(), h.db, projectID, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT id, domain, status, expires_at, dns_configured, purchase_price
		FROM purchased_domains
		WHERE team_id = $1 AND (project_id IS NULL OR project_id = $2)
		ORDER BY created_at DESC
	`, teamID, projectID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}
	defer rows.Close()

	domains := []purchasedDomainResponse{}
	for rows.Next() {
		var item purchasedDomainResponse
		if err := rows.Scan(
			&item.ID,
			&item.Domain,
			&item.Status,
			&item.ExpiresAt,
			&item.DNSConfigured,
			&item.PurchasePrice,
		); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		domains = append(domains, item)
	}
	if err := rows.Err(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"domains": domains})
}

func (h *apiDomainsHandler) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing query"})
		return
	}

	result, err := h.service.Search(r.Context(), query)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"results": result.Results})
}

func (h *apiDomainsHandler) register(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	projectID := r.PathValue("id")
	if projectID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project id"})
		return
	}

	var req struct {
		Domain string `json:"domain"`
		Years  int    `json:"years"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	teamID, err := projectTeamForUser(r.Context(), h.db, projectID, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	result, err := h.service.Purchase(r.Context(), domainsvc.PurchaseInput{
		Domain:    req.Domain,
		ProjectID: projectID,
		TeamID:    teamID,
		Years:     req.Years,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"domain":      result.Domain,
		"status":      result.Status,
		"expires_at":  result.ExpiresAt,
		"price_usd":   result.PriceUSD,
		"renewal_usd": result.RenewalUSD,
		"years":       result.Years,
		"auto_renew":  result.AutoRenew,
		"dns_status":  result.DNSStatus,
	})
}

func (h *apiDomainsHandler) attach(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	projectID := r.PathValue("id")
	if projectID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project id"})
		return
	}

	var req struct {
		Domain     string `json:"domain"`
		ServiceURL string `json:"service_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}
	if strings.TrimSpace(req.ServiceURL) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing service_url"})
		return
	}

	teamID, err := projectTeamForUser(r.Context(), h.db, projectID, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	result, err := h.service.AddDomain(r.Context(), domainsvc.AddDomainInput{
		ProjectID:  projectID,
		ServiceURL: req.ServiceURL,
		Domain:     req.Domain,
		TeamIDs:    []string{teamID},
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"domain":       result.Domain,
		"service":      result.Service,
		"status":       result.Status,
		"dns_records":  result.DNSRecords,
		"message":      result.Message,
		"instructions": result.Instructions,
	})
}

func (h *apiDomainsHandler) listDNS(w http.ResponseWriter, r *http.Request) {
	teamID, ok := h.requireProjectTeamID(w, r)
	if !ok {
		return
	}

	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	if domain == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing domain"})
		return
	}

	result, err := h.service.ListDNS(r.Context(), domain, []string{teamID})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *apiDomainsHandler) createDNS(w http.ResponseWriter, r *http.Request) {
	teamID, ok := h.requireProjectTeamID(w, r)
	if !ok {
		return
	}

	var req struct {
		Domain  string                     `json:"domain"`
		Records []domainsvc.DNSRecordInput `json:"records"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	result, err := h.service.CreateDNS(r.Context(), req.Domain, []string{teamID}, req.Records)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *apiDomainsHandler) deleteDNS(w http.ResponseWriter, r *http.Request) {
	teamID, ok := h.requireProjectTeamID(w, r)
	if !ok {
		return
	}

	var req struct {
		Domain  string                     `json:"domain"`
		Records []domainsvc.DNSRecordInput `json:"records"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	result, err := h.service.DeleteDNS(r.Context(), req.Domain, []string{teamID}, req.Records)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *apiDomainsHandler) requireProjectTeamID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := requireUserID(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return "", false
	}

	projectID := r.PathValue("id")
	if projectID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing project id"})
		return "", false
	}

	teamID, err := projectTeamForUser(r.Context(), h.db, projectID, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "project not found"})
			return "", false
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return "", false
	}

	return teamID, true
}

func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case domainsvc.IsCode(err, domainsvc.ErrInvalidArgument):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case domainsvc.IsCode(err, domainsvc.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case domainsvc.IsCode(err, domainsvc.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case domainsvc.IsCode(err, domainsvc.ErrFailedPrecondtion):
		writeJSON(w, http.StatusPaymentRequired, map[string]string{"error": err.Error()})
	case domainsvc.IsCode(err, domainsvc.ErrUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	case domainsvc.IsCode(err, domainsvc.ErrExternal):
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}
