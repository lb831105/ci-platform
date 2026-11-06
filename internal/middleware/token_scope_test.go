package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ci-platform/internal/pkg"
)

type allowAllPerms struct{}

func (allowAllPerms) HasPermission(string, string) bool { return true }

func TestRequirePermHonorsTokenScopes(t *testing.T) {
	handler := RequirePerm(allowAllPerms{}, "job.build")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	cases := []struct {
		name   string
		scopes []string
		want   int
	}{
		{"session/no scopes", nil, http.StatusNoContent},
		{"all", []string{"*"}, http.StatusNoContent},
		{"matching", []string{"job.build"}, http.StatusNoContent},
		{"missing", []string{"job.read"}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/pipelines/p1/build", nil)
			ctx := pkg.WithUser(req.Context(), "alice")
			ctx = pkg.WithTokenScopes(ctx, tc.scopes)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req.WithContext(ctx))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if tc.name == "missing" && !strings.Contains(rec.Body.String(), "40300") {
				t.Fatalf("missing scope should be rejected: %s", rec.Body.String())
			}
		})
	}
}
