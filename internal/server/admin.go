package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	gateway "github.com/eugener/gandalf/internal"
	"github.com/eugener/gandalf/internal/app"
)

// maxAdminBody is the maximum allowed admin request body size (1 MB).
const maxAdminBody = 1 << 20

// decodeJSON limits body size, decodes JSON into v, and writes a 400 on error.
// Returns true if decoding succeeded.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid request body"))
		return false
	}
	return true
}

// writeAdminError logs the full error server-side and returns a sanitized
// message to the client to avoid leaking internal details (e.g. SQLite errors).
func writeAdminError(w http.ResponseWriter, r *http.Request, err error) {
	status := errorStatus(err)
	switch {
	case errors.Is(err, gateway.ErrNotFound):
		writeJSON(w, status, errorResponse("not found"))
	case errors.Is(err, gateway.ErrConflict):
		writeJSON(w, status, errorResponse("conflict"))
	default:
		slog.LogAttrs(r.Context(), slog.LevelError, "admin error",
			slog.String("error", err.Error()),
		)
		writeJSON(w, status, errorResponse("internal error"))
	}
}

// --- Pagination helpers ---

type pagination struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
	Total  int `json:"total"`
}

type listResponse struct {
	Data       any        `json:"data"`
	Pagination pagination `json:"pagination"`
}

func parsePagination(r *http.Request) (offset, limit int) {
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return
}

// resolveOrgID returns the org_id from the query string, defaulting to the
// caller's org. Writes 403 and returns "" if the requested org differs.
func resolveOrgID(w http.ResponseWriter, r *http.Request) (string, bool) {
	identity := gateway.IdentityFromContext(r.Context())
	orgID := r.URL.Query().Get("org_id")
	if orgID == "" {
		orgID = identity.OrgID
	}
	if orgID != identity.OrgID {
		writeJSON(w, http.StatusForbidden, errorResponse("cannot access resources outside your organization"))
		return "", false
	}
	return orgID, true
}

// parseSinceUntil validates optional since/until RFC3339 query params.
// Writes 400 and returns false on invalid format.
func parseSinceUntil(w http.ResponseWriter, r *http.Request) (since, until string, ok bool) {
	q := r.URL.Query()
	since, until = q.Get("since"), q.Get("until")
	// Validate RFC3339 upfront: SQLite datetime() silently returns NULL on
	// malformed strings, producing empty results instead of a clear error.
	if since != "" {
		if _, err := time.Parse(time.RFC3339, since); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse("invalid since format, use RFC3339"))
			return "", "", false
		}
	}
	if until != "" {
		if _, err := time.Parse(time.RFC3339, until); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse("invalid until format, use RFC3339"))
			return "", "", false
		}
	}
	return since, until, true
}

// parseExpiresAt parses an optional RFC3339 expires_at string pointer.
// Writes 400 and returns false on invalid format.
func parseExpiresAt(w http.ResponseWriter, raw *string) (*time.Time, bool) {
	if raw == nil {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid expires_at format"))
		return nil, false
	}
	return &t, true
}

// --- Providers ---

func (s *server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := s.deps.Store.ListProviders(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse("failed to list providers"))
		return
	}
	total, _ := s.deps.Store.CountProviders(r.Context())
	if providers == nil {
		providers = []*gateway.ProviderConfig{}
	}
	writeJSON(w, http.StatusOK, listResponse{
		Data:       providers,
		Pagination: pagination{Offset: 0, Limit: len(providers), Total: total},
	})
}

func (s *server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var p gateway.ProviderConfig
	if !decodeJSON(w, r, &p) {
		return
	}
	p.APIKeyEnc = "" // defense-in-depth: strip even though json:"-"
	if p.Name == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse("name is required"))
		return
	}
	if p.ID == "" {
		p.ID = p.Name
	}
	if err := s.deps.Store.CreateProvider(r.Context(), &p); err != nil {
		writeAdminError(w, r, err)
		return
	}
	w.Header().Set("Location", "/admin/v1/providers/"+p.ID)
	writeJSON(w, http.StatusCreated, p)
}

func (s *server) handleGetProvider(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, err := s.deps.Store.GetProvider(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var p gateway.ProviderConfig
	if !decodeJSON(w, r, &p) {
		return
	}
	p.APIKeyEnc = "" // defense-in-depth: strip even though json:"-"
	p.ID = id
	if err := s.deps.Store.UpdateProvider(r.Context(), &p); err != nil {
		writeAdminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.deps.Store.DeleteProvider(r.Context(), id); err != nil {
		writeAdminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Keys ---

// keyCreateRequest is the payload for creating a new API key.
type keyCreateRequest struct {
	OrgID         string   `json:"org_id"`
	UserID        string   `json:"user_id,omitempty"`
	TeamID        string   `json:"team_id,omitempty"`
	Role          string   `json:"role,omitempty"`
	AllowedModels []string `json:"allowed_models,omitempty"`
	RPMLimit      *int64   `json:"rpm_limit,omitempty"`
	TPMLimit      *int64   `json:"tpm_limit,omitempty"`
	MaxBudget     *float64 `json:"max_budget,omitempty"`
	ExpiresAt     *string  `json:"expires_at,omitempty"` // RFC3339
}

// keyCreateResponse includes the plaintext key (shown only once).
type keyCreateResponse struct {
	*gateway.APIKey
	PlaintextKey string `json:"key"`
}

func (s *server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	orgID, ok := resolveOrgID(w, r)
	if !ok {
		return
	}
	offset, limit := parsePagination(r)

	keys, err := s.deps.Store.ListKeys(r.Context(), orgID, offset, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse("failed to list keys"))
		return
	}
	total, _ := s.deps.Store.CountKeys(r.Context(), orgID)
	if keys == nil {
		keys = []*gateway.APIKey{}
	}
	writeJSON(w, http.StatusOK, listResponse{
		Data:       keys,
		Pagination: pagination{Offset: offset, Limit: limit, Total: total},
	})
}

func (s *server) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	var req keyCreateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// Reject unknown roles early to prevent storing invalid data in DB.
	if req.Role != "" && !gateway.ValidRole(req.Role) {
		writeJSON(w, http.StatusBadRequest, errorResponse("invalid role"))
		return
	}
	identity := gateway.IdentityFromContext(r.Context())
	if req.OrgID == "" {
		req.OrgID = identity.OrgID
	}
	if req.OrgID != identity.OrgID {
		writeJSON(w, http.StatusForbidden, errorResponse("cannot create keys outside your organization"))
		return
	}

	expiresAt, ok := parseExpiresAt(w, req.ExpiresAt)
	if !ok {
		return
	}

	plaintext, key, err := s.deps.Keys.CreateKey(r.Context(), app.CreateKeyOpts{
		OrgID:         req.OrgID,
		UserID:        req.UserID,
		TeamID:        req.TeamID,
		Role:          req.Role,
		AllowedModels: req.AllowedModels,
		RPMLimit:      req.RPMLimit,
		TPMLimit:      req.TPMLimit,
		MaxBudget:     req.MaxBudget,
		ExpiresAt:     expiresAt,
	})
	if err != nil {
		writeAdminError(w, r, err)
		return
	}

	w.Header().Set("Location", "/admin/v1/keys/"+key.ID)
	writeJSON(w, http.StatusCreated, keyCreateResponse{
		APIKey:       key,
		PlaintextKey: plaintext,
	})
}

func (s *server) handleGetKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	key, err := s.deps.Store.GetKey(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, err)
		return
	}
	identity := gateway.IdentityFromContext(r.Context())
	if key.OrgID != identity.OrgID {
		writeJSON(w, http.StatusNotFound, errorResponse("not found"))
		return
	}
	writeJSON(w, http.StatusOK, key)
}

func (s *server) handleUpdateKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := s.deps.Store.GetKey(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, err)
		return
	}
	identity := gateway.IdentityFromContext(r.Context())
	if existing.OrgID != identity.OrgID {
		writeJSON(w, http.StatusNotFound, errorResponse("not found"))
		return
	}

	// Decode update payload on top of existing.
	var update struct {
		Role          *string  `json:"role,omitempty"`
		AllowedModels []string `json:"allowed_models,omitempty"`
		RPMLimit      *int64   `json:"rpm_limit,omitempty"`
		TPMLimit      *int64   `json:"tpm_limit,omitempty"`
		MaxBudget     *float64 `json:"max_budget,omitempty"`
		ExpiresAt     *string  `json:"expires_at,omitempty"`
		Blocked       *bool    `json:"blocked,omitempty"`
	}
	if !decodeJSON(w, r, &update) {
		return
	}

	// Reject unknown roles early to prevent storing invalid data in DB.
	if update.Role != nil {
		if !gateway.ValidRole(*update.Role) {
			writeJSON(w, http.StatusBadRequest, errorResponse("invalid role"))
			return
		}
		existing.Role = *update.Role
	}
	if update.AllowedModels != nil {
		existing.AllowedModels = update.AllowedModels
	}
	if update.RPMLimit != nil {
		existing.RPMLimit = update.RPMLimit
	}
	if update.TPMLimit != nil {
		existing.TPMLimit = update.TPMLimit
	}
	if update.MaxBudget != nil {
		existing.MaxBudget = update.MaxBudget
	}
	if update.ExpiresAt != nil {
		expiresAt, ok := parseExpiresAt(w, update.ExpiresAt)
		if !ok {
			return
		}
		existing.ExpiresAt = expiresAt
	}
	if update.Blocked != nil {
		existing.Blocked = *update.Blocked
	}

	if err := s.deps.Store.UpdateKey(r.Context(), existing); err != nil {
		writeAdminError(w, r, err)
		return
	}
	if s.deps.KeyInvalidator != nil {
		s.deps.KeyInvalidator.InvalidateByKeyID(id)
	}
	writeJSON(w, http.StatusOK, existing)
}

func (s *server) handleDeleteKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	key, err := s.deps.Store.GetKey(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, err)
		return
	}
	identity := gateway.IdentityFromContext(r.Context())
	if key.OrgID != identity.OrgID {
		writeJSON(w, http.StatusNotFound, errorResponse("not found"))
		return
	}
	if err := s.deps.Store.DeleteKey(r.Context(), id); err != nil {
		writeAdminError(w, r, err)
		return
	}
	if s.deps.KeyInvalidator != nil {
		s.deps.KeyInvalidator.InvalidateByKeyID(id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Routes ---

func (s *server) handleListRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := s.deps.Store.ListRoutes(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse("failed to list routes"))
		return
	}
	total, _ := s.deps.Store.CountRoutes(r.Context())
	if routes == nil {
		routes = []*gateway.Route{}
	}
	writeJSON(w, http.StatusOK, listResponse{
		Data:       routes,
		Pagination: pagination{Offset: 0, Limit: len(routes), Total: total},
	})
}

func (s *server) handleCreateRoute(w http.ResponseWriter, r *http.Request) {
	var route gateway.Route
	if !decodeJSON(w, r, &route) {
		return
	}
	if route.ModelAlias == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse("model_alias is required"))
		return
	}
	if route.ID == "" {
		route.ID = uuid.Must(uuid.NewV7()).String()
	}
	if route.Strategy == "" {
		route.Strategy = "priority"
	}
	if err := s.deps.Store.CreateRoute(r.Context(), &route); err != nil {
		writeAdminError(w, r, err)
		return
	}
	w.Header().Set("Location", "/admin/v1/routes/"+route.ID)
	writeJSON(w, http.StatusCreated, route)
}

func (s *server) handleGetRoute(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	route, err := s.deps.Store.GetRoute(r.Context(), id)
	if err != nil {
		writeAdminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func (s *server) handleUpdateRoute(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var route gateway.Route
	if !decodeJSON(w, r, &route) {
		return
	}
	route.ID = id
	if err := s.deps.Store.UpdateRoute(r.Context(), &route); err != nil {
		writeAdminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func (s *server) handleDeleteRoute(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.deps.Store.DeleteRoute(r.Context(), id); err != nil {
		writeAdminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Cache ---

func (s *server) handleCachePurge(w http.ResponseWriter, r *http.Request) {
	if s.deps.Cache != nil {
		s.deps.Cache.Purge(r.Context())
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Usage ---

func (s *server) handleQueryUsage(w http.ResponseWriter, r *http.Request) {
	orgID, ok := resolveOrgID(w, r)
	if !ok {
		return
	}
	since, until, ok := parseSinceUntil(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	offset, limit := parsePagination(r)
	filter := gateway.UsageFilter{
		OrgID:  orgID,
		KeyID:  q.Get("key_id"),
		Model:  q.Get("model"),
		Since:  since,
		Until:  until,
		Offset: offset,
		Limit:  limit,
	}
	records, err := s.deps.Store.QueryUsage(r.Context(), filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse("failed to query usage"))
		return
	}
	total, _ := s.deps.Store.CountUsage(r.Context(), filter)
	if records == nil {
		records = []gateway.UsageRecord{}
	}
	writeJSON(w, http.StatusOK, listResponse{
		Data:       records,
		Pagination: pagination{Offset: offset, Limit: limit, Total: total},
	})
}

func (s *server) handleUsageSummary(w http.ResponseWriter, r *http.Request) {
	orgID, ok := resolveOrgID(w, r)
	if !ok {
		return
	}
	since, until, ok := parseSinceUntil(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	filter := gateway.RollupFilter{
		OrgID:  orgID,
		KeyID:  q.Get("key_id"),
		Model:  q.Get("model"),
		Period: q.Get("period"),
		Since:  since,
		Until:  until,
	}
	rollups, err := s.deps.Store.QueryRollups(r.Context(), filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse("failed to query rollups"))
		return
	}
	if rollups == nil {
		rollups = []gateway.UsageRollup{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rollups})
}
