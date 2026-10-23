package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// CredentialHandler 处理凭据的增删改查。
type CredentialHandler struct {
	credentials *service.CredentialService
}

// NewCredentialHandler 构造凭据处理器。
func NewCredentialHandler(credentials *service.CredentialService) *CredentialHandler {
	return &CredentialHandler{credentials: credentials}
}

// List GET /api/credentials
func (h *CredentialHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.credentials.List(r.Context())
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, items)
}

// Get GET /api/credentials/{id}
func (h *CredentialHandler) Get(w http.ResponseWriter, r *http.Request) {
	item, err := h.credentials.Get(r.Context(), chi.URLParam(r, "cid"))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, item)
}

// Create POST /api/credentials
func (h *CredentialHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in model.Credential
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	created, err := h.credentials.Create(r.Context(), &in)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, created)
}

// Update PUT /api/credentials/{id}
func (h *CredentialHandler) Update(w http.ResponseWriter, r *http.Request) {
	var in model.Credential
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	updated, err := h.credentials.Update(r.Context(), chi.URLParam(r, "cid"), &in)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, updated)
}

// Delete DELETE /api/credentials/{id}
func (h *CredentialHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.credentials.Delete(r.Context(), chi.URLParam(r, "cid")); err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, nil)
}
