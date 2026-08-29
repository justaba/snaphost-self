package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// fakeStore records the filter it was called with and returns canned data.
// Methods a test does not exercise panic, so an unexpected call is loud.
type fakeStore struct {
	gotFilter Filter
	users     *Page[UserSummary]
	deploys   *Page[DeployRow]
	keys      []APIKeyRow
	userErr   error
	listErr   error
}

func (f *fakeStore) Overview(context.Context) (*Overview, error) {
	return &Overview{Users: 3, TotalBalance: 250}, nil
}

func (f *fakeStore) ListUsers(_ context.Context, flt Filter) (*Page[UserSummary], error) {
	f.gotFilter = flt
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.users != nil {
		return f.users, nil
	}
	return &Page[UserSummary]{Items: []UserSummary{}, Limit: flt.Limit, Offset: flt.Offset}, nil
}

func (f *fakeStore) GetUser(_ context.Context, userID uuid.UUID) (*UserDetail, error) {
	if f.userErr != nil {
		return nil, f.userErr
	}
	return &UserDetail{
		UserSummary: UserSummary{ID: userID, HasWallet: true},
		Projects:    []ProjectRow{},
		Domains:     []DomainRow{},
		APIKeys:     f.keys,
	}, nil
}

func (f *fakeStore) ListDeploys(_ context.Context, flt Filter) (*Page[DeployRow], error) {
	f.gotFilter = flt
	if f.deploys != nil {
		return f.deploys, nil
	}
	return &Page[DeployRow]{Items: []DeployRow{}, Limit: flt.Limit, Offset: flt.Offset}, nil
}

func (f *fakeStore) GetDeploy(context.Context, uuid.UUID) (*DeployDetail, error) {
	panic("not used")
}

func (f *fakeStore) ListTransactions(_ context.Context, flt Filter) (*Page[LedgerRow], error) {
	f.gotFilter = flt
	return &Page[LedgerRow]{Items: []LedgerRow{}, Limit: flt.Limit, Offset: flt.Offset}, nil
}

func (f *fakeStore) ListDomains(_ context.Context, flt Filter) (*Page[DomainRow], error) {
	f.gotFilter = flt
	return &Page[DomainRow]{Items: []DomainRow{}, Limit: flt.Limit, Offset: flt.Offset}, nil
}

func (f *fakeStore) ListProjects(context.Context, uuid.UUID) ([]ProjectRow, error) {
	return []ProjectRow{}, nil
}

func (f *fakeStore) ListAPIKeys(context.Context, uuid.UUID) ([]APIKeyRow, error) {
	return f.keys, nil
}

// newTestRouter wires the admin routes exactly as routes.Register does,
// including the role gate, so the tests exercise the real chain.
func newTestRouter(store Store) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewHandler(store, zap.NewNop())
	r := gin.New()
	adm := r.Group("/api/v1/admin", RequireAdmin())
	adm.GET("/overview", h.Overview)
	adm.GET("/users", h.ListUsers)
	adm.GET("/users/:id", h.GetUser)
	adm.GET("/users/:id/keys", h.UserKeys)
	adm.GET("/deploys", h.ListDeploys)
	adm.GET("/transactions", h.ListTransactions)
	return r
}

func do(r *gin.Engine, method, path, role string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if role != "" {
		req.Header.Set(RoleHeader, role)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRequireAdmin_RefusesNonAdmins(t *testing.T) {
	r := newTestRouter(&fakeStore{})

	cases := []struct {
		name string
		role string
	}{
		{"no role header at all", ""},
		{"ordinary user", "user"},
		{"guest", "guest"},
		{"empty value", "   "},
		{"lookalike", "administrator"},
		{"spoof attempt with suffix", "admin,user"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := do(r, http.MethodGet, "/api/v1/admin/users", tc.role)
			if w.Code != http.StatusForbidden {
				t.Fatalf("role %q: got %d want %d", tc.role, w.Code, http.StatusForbidden)
			}
			if !strings.Contains(w.Body.String(), "forbidden") {
				t.Fatalf("body %q does not name the refusal", w.Body.String())
			}
		})
	}
}

func TestRequireAdmin_AdmitsAdmin(t *testing.T) {
	r := newTestRouter(&fakeStore{})

	// The role comes from a JWT claim written by a Postgres hook; casing is
	// not something this service should depend on.
	for _, role := range []string{"admin", "Admin", "ADMIN", " admin "} {
		w := do(r, http.MethodGet, "/api/v1/admin/overview", role)
		if w.Code != http.StatusOK {
			t.Fatalf("role %q: got %d want 200 (%s)", role, w.Code, w.Body.String())
		}
	}
}

func TestListUsers_ClampsPagination(t *testing.T) {
	cases := []struct {
		query      string
		wantLimit  int
		wantOffset int
	}{
		{"", DefaultLimit, 0},
		{"?limit=10&offset=20", 10, 20},
		{"?limit=0", DefaultLimit, 0},
		{"?limit=-5", DefaultLimit, 0},
		{"?limit=99999", MaxLimit, 0},
		{"?limit=abc", DefaultLimit, 0},
		{"?offset=-1", DefaultLimit, 0},
	}

	for _, tc := range cases {
		t.Run("q"+tc.query, func(t *testing.T) {
			store := &fakeStore{}
			r := newTestRouter(store)

			w := do(r, http.MethodGet, "/api/v1/admin/users"+tc.query, "admin")
			if w.Code != http.StatusOK {
				t.Fatalf("got %d want 200", w.Code)
			}
			if store.gotFilter.Limit != tc.wantLimit {
				t.Errorf("limit: got %d want %d", store.gotFilter.Limit, tc.wantLimit)
			}
			if store.gotFilter.Offset != tc.wantOffset {
				t.Errorf("offset: got %d want %d", store.gotFilter.Offset, tc.wantOffset)
			}
		})
	}
}

func TestListDeploys_PassesFilters(t *testing.T) {
	store := &fakeStore{}
	r := newTestRouter(store)
	userID := uuid.MustParse("11111111-2222-3333-4444-555555555555")

	w := do(r, http.MethodGet,
		"/api/v1/admin/deploys?status=running&user_id="+userID.String()+"&q=example.com", "admin")
	if w.Code != http.StatusOK {
		t.Fatalf("got %d want 200", w.Code)
	}

	if store.gotFilter.Status != "running" {
		t.Errorf("status: got %q want running", store.gotFilter.Status)
	}
	if store.gotFilter.Query != "example.com" {
		t.Errorf("query: got %q want example.com", store.gotFilter.Query)
	}
	if store.gotFilter.UserID == nil || *store.gotFilter.UserID != userID {
		t.Errorf("user_id: got %v want %s", store.gotFilter.UserID, userID)
	}
}

func TestListDeploys_MalformedUserIDIsIgnored(t *testing.T) {
	store := &fakeStore{}
	r := newTestRouter(store)

	// A list screen should render rather than 400 on a bad filter value.
	w := do(r, http.MethodGet, "/api/v1/admin/deploys?user_id=not-a-uuid", "admin")
	if w.Code != http.StatusOK {
		t.Fatalf("got %d want 200", w.Code)
	}
	if store.gotFilter.UserID != nil {
		t.Fatalf("expected no user filter, got %v", store.gotFilter.UserID)
	}
}

func TestGetUser_MalformedIDIsRejected(t *testing.T) {
	r := newTestRouter(&fakeStore{})

	w := do(r, http.MethodGet, "/api/v1/admin/users/not-a-uuid", "admin")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d want 400", w.Code)
	}
}

func TestGetUser_NotFound(t *testing.T) {
	r := newTestRouter(&fakeStore{userErr: ErrNotFound})

	w := do(r, http.MethodGet, "/api/v1/admin/users/"+uuid.New().String(), "admin")
	if w.Code != http.StatusNotFound {
		t.Fatalf("got %d want 404 (%s)", w.Code, w.Body.String())
	}
}

func TestListUsers_DatabaseErrorIsNotLeaked(t *testing.T) {
	dbErr := errors.New(`ERROR: column "secret_column" does not exist (SQLSTATE 42703)`)
	r := newTestRouter(&fakeStore{listErr: dbErr})

	w := do(r, http.MethodGet, "/api/v1/admin/users", "admin")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("got %d want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), "secret_column") || strings.Contains(w.Body.String(), "SQLSTATE") {
		t.Fatalf("database error reached the response body: %s", w.Body.String())
	}
}

// The key hash is the only stored credential in this database. It has no field
// on APIKeyRow, and this test is what keeps it that way if the type is ever
// widened by copying from apikey.Repository.
func TestUserKeys_NeverSerialisesSecretMaterial(t *testing.T) {
	store := &fakeStore{keys: []APIKeyRow{{
		ID:     uuid.New(),
		UserID: uuid.New(),
		Prefix: "sk_live_abcd",
		Name:   "mcp",
	}}}
	r := newTestRouter(store)

	w := do(r, http.MethodGet, "/api/v1/admin/users/"+uuid.New().String()+"/keys", "admin")
	if w.Code != http.StatusOK {
		t.Fatalf("got %d want 200", w.Code)
	}

	body := strings.ToLower(w.Body.String())
	for _, forbidden := range []string{"key_hash", "hash", "secret", "token"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response carries %q: %s", forbidden, w.Body.String())
		}
	}
}

func TestPageShapeIsStable(t *testing.T) {
	store := &fakeStore{users: &Page[UserSummary]{
		Items:  []UserSummary{{ID: uuid.New()}},
		Total:  42,
		Limit:  50,
		Offset: 0,
	}}
	r := newTestRouter(store)

	w := do(r, http.MethodGet, "/api/v1/admin/users", "admin")

	var page struct {
		Items  []json.RawMessage `json:"items"`
		Total  int64             `json:"total"`
		Limit  int               `json:"limit"`
		Offset int               `json:"offset"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v — body %s", err, w.Body.String())
	}
	if page.Total != 42 || page.Limit != 50 || len(page.Items) != 1 {
		t.Fatalf("unexpected page: total=%d limit=%d items=%d", page.Total, page.Limit, len(page.Items))
	}
}

// An empty list must serialise as [] rather than null: the dashboard maps over
// it directly, and null is a runtime error there rather than an empty table.
func TestEmptyListSerialisesAsArray(t *testing.T) {
	r := newTestRouter(&fakeStore{})

	w := do(r, http.MethodGet, "/api/v1/admin/transactions", "admin")
	if !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatalf("empty page did not serialise items as []: %s", w.Body.String())
	}
}
