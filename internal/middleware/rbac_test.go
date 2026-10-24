package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

type fakePermChecker struct {
	allow map[string]bool
}

func (f *fakePermChecker) HasPermission(user, perm string) bool {
	return f.allow[user+"/"+perm]
}

type fakeJobPermChecker struct {
	allow map[string]bool
}

func (f *fakeJobPermChecker) HasJobPermission(user, action, jobName string) bool {
	return f.allow[user+"/"+action+"/"+jobName]
}

type fakeJobNameLookup struct {
	names map[string]string
	err   error
}

func (f *fakeJobNameLookup) JobName(_ context.Context, id string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.names[id], nil
}

func TestRequirePermDeniesWithoutPermission(t *testing.T) {
	called := false
	handler := RequirePerm(&fakePermChecker{allow: map[string]bool{}}, model.PermSystemManage)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(pkg.WithUser(req.Context(), "alice"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if called {
		t.Fatal("handler must not be called without permission")
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":40300`) {
		t.Fatalf("response = %s", body)
	}
}

func TestRequirePermAllowsWithPermission(t *testing.T) {
	called := false
	handler := RequirePerm(&fakePermChecker{allow: map[string]bool{"root/" + model.PermSystemManage: true}}, model.PermSystemManage)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(pkg.WithUser(req.Context(), "root"))
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Fatal("handler must be called with permission")
	}
}

func TestRequirePermNilRBACPasses(t *testing.T) {
	called := false
	handler := RequirePerm(nil, model.PermSystemManage)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !called {
		t.Fatal("nil rbac must pass")
	}
}

func requestWithChiParam(t *testing.T, user, pid string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/pipelines/"+pid+"/build", nil)
	req = req.WithContext(pkg.WithUser(req.Context(), user))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("pid", pid)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestRequireJobPermMatchesJobName(t *testing.T) {
	called := false
	handler := RequireJobPerm(&fakeJobPermChecker{allow: map[string]bool{
		"bob/" + model.PermJobBuild + "/as-doc": true,
	}}, &fakeJobNameLookup{names: map[string]string{"p1": "as-doc"}}, model.PermJobBuild)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	handler.ServeHTTP(httptest.NewRecorder(), requestWithChiParam(t, "bob", "p1"))
	if !called {
		t.Fatal("handler must be called when job pattern matches")
	}
}

func TestRequireJobPermDeniesOtherJob(t *testing.T) {
	called := false
	handler := RequireJobPerm(&fakeJobPermChecker{allow: map[string]bool{
		"bob/" + model.PermJobBuild + "/as-doc": true,
	}}, &fakeJobNameLookup{names: map[string]string{"p1": "other-job"}}, model.PermJobBuild)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requestWithChiParam(t, "bob", "p1"))
	if called {
		t.Fatal("handler must not be called for a different job")
	}
	if !strings.Contains(rec.Body.String(), `"code":40300`) {
		t.Fatalf("response = %s", rec.Body.String())
	}
}
