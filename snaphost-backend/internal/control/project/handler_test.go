package project

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// recordingCleaner is the host side of a project deletion. What matters is
// which calls it received and what the handler did with a refusal: after the
// commit there is nothing left naming a container, so a swallowed failure
// leaks one permanently.
type recordingCleaner struct {
	calls     []string
	stopErr   error
	removeErr error
}

func (c *recordingCleaner) StopStrict(_ context.Context, deployID, containerID string) error {
	c.calls = append(c.calls, "stop:"+deployID+":"+containerID)
	return c.stopErr
}

func (c *recordingCleaner) RemoveImage(_ context.Context, deployID, imageRef string) error {
	c.calls = append(c.calls, "remove:"+deployID+":"+imageRef)
	return c.removeErr
}

type fakeStore struct {
	plan       *Deletion
	planErr    error
	deleteErr  error
	deleted    uuid.UUID
	actor      uuid.UUID
	gotPlanned []uuid.UUID
}

func (f *fakeStore) ListSummaries(context.Context, uuid.UUID) ([]Summary, error) {
	return nil, nil
}

func (f *fakeStore) PrepareDeletion(context.Context, uuid.UUID) (*Deletion, error) {
	if f.planErr != nil {
		return nil, f.planErr
	}
	if f.plan == nil {
		return nil, ErrNotFound
	}
	return f.plan, nil
}

func (f *fakeStore) Delete(_ context.Context, projectID, actorUserID uuid.UUID, planned []uuid.UUID) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = projectID
	f.actor = actorUserID
	f.gotPlanned = planned
	return nil
}

func (f *fakeStore) ListAudit(context.Context, int) ([]AuditEntry, error) { return nil, nil }

const testOwner = "b6a5d4c3-2e1f-4a3b-8c7d-6e5f4a3b2c1d"

func planFor(owner uuid.UUID, deploys ...DeployResource) *Deletion {
	return &Deletion{Project: Summary{UserID: owner}, Deploys: deploys}
}

func newRouter(store Store, runtime RuntimeCleaner) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := NewHandler(store, runtime, zap.NewNop())
	r := gin.New()
	r.DELETE("/api/v1/projects/:id", h.Delete)
	return r
}

func deleteProject(r *gin.Engine, projectID, actor string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+projectID, nil)
	if actor != "" {
		req.Header.Set(userIDHeader, actor)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// The refusal has to reach the handler. StopStrict exists because the ordinary
// Stop reports success for a deploy the runtime will not recognise — safe for
// saga compensation, unsafe here — and the Docker backend used to swallow a
// failed container removal on top of that. Two layers of "it probably worked"
// in front of an irreversible delete.
func TestDeleteKeepsEveryRowWhenTheContainerWillNotGo(t *testing.T) {
	owner := uuid.MustParse(testOwner)
	store := &fakeStore{plan: planFor(owner, DeployResource{
		DeployID:    uuid.New(),
		Status:      "running",
		ContainerID: "container-1",
		ImageRef:    "snaphost/proj-abc:1",
	})}
	cleaner := &recordingCleaner{stopErr: errors.New("device or resource busy")}

	w := deleteProject(newRouter(store, cleaner), uuid.NewString(), owner.String())

	if w.Code != http.StatusBadGateway {
		t.Fatalf("got %d (%s), want 502", w.Code, w.Body.String())
	}
	if store.deleted != uuid.Nil {
		t.Fatal("rows were deleted while a container was still on the host")
	}
	if !strings.Contains(w.Body.String(), "nothing was deleted") {
		t.Errorf("body %q does not tell the operator the state is unchanged", w.Body.String())
	}
}

func TestDeleteKeepsEveryRowWhenTheImageWillNotGo(t *testing.T) {
	owner := uuid.MustParse(testOwner)
	store := &fakeStore{plan: planFor(owner, DeployResource{
		DeployID: uuid.New(),
		Status:   "failed",
		ImageRef: "snaphost/proj-abc:1",
	})}
	cleaner := &recordingCleaner{removeErr: errors.New("image is referenced")}

	w := deleteProject(newRouter(store, cleaner), uuid.NewString(), owner.String())

	if w.Code != http.StatusBadGateway {
		t.Fatalf("got %d (%s), want 502", w.Code, w.Body.String())
	}
	if store.deleted != uuid.Nil {
		t.Fatal("rows were deleted while an image was still on disk")
	}
}

// Stopping no longer releases the image — a stopped deploy has to stay
// startable — so this caller has to ask for both, in that order.
func TestDeleteStopsAndRemovesForTheSameDeploy(t *testing.T) {
	owner := uuid.MustParse(testOwner)
	deployID := uuid.New()
	store := &fakeStore{plan: planFor(owner, DeployResource{
		DeployID:    deployID,
		Status:      "running",
		ContainerID: "container-1",
		ImageRef:    "snaphost/proj-abc:1",
	})}
	cleaner := &recordingCleaner{}
	projectID := uuid.New()

	w := deleteProject(newRouter(store, cleaner), projectID.String(), owner.String())
	if w.Code != http.StatusNoContent {
		t.Fatalf("got %d (%s), want 204", w.Code, w.Body.String())
	}

	want := []string{
		"stop:" + deployID.String() + ":container-1",
		"remove:" + deployID.String() + ":snaphost/proj-abc:1",
	}
	if len(cleaner.calls) != 2 || cleaner.calls[0] != want[0] || cleaner.calls[1] != want[1] {
		t.Fatalf("cleanup calls = %v, want %v", cleaner.calls, want)
	}
	if store.deleted != projectID {
		t.Errorf("deleted %v, want %v", store.deleted, projectID)
	}
}

// The commit needs the exact set the cleanup acted on: anything the project
// has gained since is a deploy nothing stopped.
func TestDeletePassesTheCleanedSetToTheCommit(t *testing.T) {
	owner := uuid.MustParse(testOwner)
	a, b := uuid.New(), uuid.New()
	store := &fakeStore{plan: planFor(owner,
		DeployResource{DeployID: a, Status: "stopped", ImageRef: "snaphost/proj-abc:a"},
		DeployResource{DeployID: b, Status: "failed", ImageRef: "snaphost/proj-abc:b"},
	)}

	w := deleteProject(newRouter(store, &recordingCleaner{}), uuid.NewString(), owner.String())
	if w.Code != http.StatusNoContent {
		t.Fatalf("got %d (%s), want 204", w.Code, w.Body.String())
	}
	if len(store.gotPlanned) != 2 || store.gotPlanned[0] != a || store.gotPlanned[1] != b {
		t.Fatalf("planned set = %v, want [%v %v]", store.gotPlanned, a, b)
	}
}

func TestDeleteRefusesAnotherAccountsProject(t *testing.T) {
	store := &fakeStore{plan: planFor(uuid.New())}

	w := deleteProject(newRouter(store, &recordingCleaner{}), uuid.NewString(), testOwner)
	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d (%s), want 403", w.Code, w.Body.String())
	}
	if store.deleted != uuid.Nil {
		t.Fatal("another account's project was deleted")
	}
}

// The audit row's whole value is the actor. A deletion that cannot name one
// must not happen rather than be recorded against nobody.
func TestDeleteRefusesWithoutAnIdentifiedActor(t *testing.T) {
	for _, actor := range []string{"", "   ", "not-a-uuid"} {
		store := &fakeStore{plan: planFor(uuid.MustParse(testOwner))}
		w := deleteProject(newRouter(store, &recordingCleaner{}), uuid.NewString(), actor)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("actor %q: got %d, want 401", actor, w.Code)
		}
		if store.deleted != uuid.Nil {
			t.Fatalf("actor %q: deleted with nobody to attribute it to", actor)
		}
	}
}

func TestDeleteStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
		code string
	}{
		{"unknown project", ErrNotFound, http.StatusNotFound, "project_not_found"},
		{"work in flight", ErrBusy, http.StatusConflict, "project_busy"},
		{"anything else", errors.New("disk i/o error"), http.StatusInternalServerError, "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{planErr: tc.err}
			w := deleteProject(newRouter(store, &recordingCleaner{}), uuid.NewString(), testOwner)
			if w.Code != tc.want {
				t.Fatalf("got %d (%s), want %d", w.Code, w.Body.String(), tc.want)
			}
			if !strings.Contains(w.Body.String(), tc.code) {
				t.Errorf("body %q does not carry %q", w.Body.String(), tc.code)
			}
		})
	}
}

// A commit refused by the race check must surface as 409, not as a success the
// operator reads as "done".
func TestDeleteSurfacesACommitRefusal(t *testing.T) {
	owner := uuid.MustParse(testOwner)
	store := &fakeStore{plan: planFor(owner), deleteErr: ErrBusy}

	w := deleteProject(newRouter(store, &recordingCleaner{}), uuid.NewString(), owner.String())
	if w.Code != http.StatusConflict {
		t.Fatalf("got %d (%s), want 409", w.Code, w.Body.String())
	}
}

// A project holding host state with no runtime configured must say so rather
// than reach a nil client. The pre-check and the loop have to agree about what
// counts as host state.
func TestDeleteWithoutARuntimeClient(t *testing.T) {
	owner := uuid.MustParse(testOwner)

	t.Run("nothing to clean up", func(t *testing.T) {
		store := &fakeStore{plan: planFor(owner, DeployResource{DeployID: uuid.New(), Status: "failed"})}
		w := deleteProject(newRouter(store, nil), uuid.NewString(), owner.String())
		if w.Code != http.StatusNoContent {
			t.Fatalf("got %d (%s), want 204", w.Code, w.Body.String())
		}
	})

	// An already-swept deploy keeps its image_ref for diagnostics, so the
	// pre-check cannot use "has an image_ref and no marker" and the loop use
	// "has an image_ref" — that disagreement is a nil dereference.
	t.Run("image already swept", func(t *testing.T) {
		store := &fakeStore{plan: planFor(owner, DeployResource{
			DeployID: uuid.New(), Status: "deleted",
			ImageRef: "snaphost/proj-abc:1", ImageDeleted: true,
		})}
		w := deleteProject(newRouter(store, nil), uuid.NewString(), owner.String())
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("got %d (%s), want 503 rather than a panic", w.Code, w.Body.String())
		}
	})
}
