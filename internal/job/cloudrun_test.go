package job

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	runpb "cloud.google.com/go/run/apiv2/runpb"
)

type fakeCloudRunClient struct {
	mu     sync.Mutex
	reqs   []*runpb.RunJobRequest
	ctxs   []context.Context
	err    error
	closed bool
	run    func(ctx context.Context, req *runpb.RunJobRequest) error
}

func (f *fakeCloudRunClient) RunJob(ctx context.Context, req *runpb.RunJobRequest) error {
	if f.run != nil {
		return f.run(ctx, req)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	f.ctxs = append(f.ctxs, ctx)
	return f.err
}

func (f *fakeCloudRunClient) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeCloudRunClient) last() *runpb.RunJobRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		return nil
	}
	return f.reqs[len(f.reqs)-1]
}

func TestCloudRunJobName(t *testing.T) {
	got, err := cloudRunJobName("demo-proj", "europe-west1", "podcaster-worker")
	if err != nil {
		t.Fatal(err)
	}
	want := "projects/demo-proj/locations/europe-west1/jobs/podcaster-worker"
	if got != want {
		t.Fatalf("name %q, want %q", got, want)
	}
}

func TestCloudRunJobNameRejectsInvalid(t *testing.T) {
	cases := []struct {
		name, project, region, job string
	}{
		{"empty project", "", "us-central1", "podcaster-worker"},
		{"empty region", "demo", "", "podcaster-worker"},
		{"empty job", "demo", "us-central1", ""},
		{"whitespace", "  ", "us-central1", "podcaster-worker"},
		{"full resource path as job", "demo", "us-central1", "projects/demo/locations/us-central1/jobs/podcaster-worker"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cloudRunJobName(tc.project, tc.region, tc.job)
			if !errors.Is(err, ErrInvalidJobIdentity) {
				t.Fatalf("got %v, want ErrInvalidJobIdentity", err)
			}
		})
	}
}

func TestNewCloudRunValidatesIdentityBeforeDial(t *testing.T) {
	// Must fail before constructing a GAPIC client (no credentials needed).
	_, err := NewCloudRun(context.Background(), "", "us-central1", "podcaster-worker")
	if !errors.Is(err, ErrInvalidJobIdentity) {
		t.Fatalf("got %v, want ErrInvalidJobIdentity", err)
	}
}

func TestCloudRunEnqueueSetsEpisodeOverride(t *testing.T) {
	fake := &fakeCloudRunClient{}
	d := newCloudRunDispatcher("projects/p/locations/r/jobs/j", fake)

	if err := d.Enqueue(context.Background(), "ep_01ja2b3c4d5e6f"); err != nil {
		t.Fatal(err)
	}
	req := fake.last()
	if req == nil {
		t.Fatal("RunJob was not called")
	}
	if req.Name != "projects/p/locations/r/jobs/j" {
		t.Fatalf("job name %q", req.Name)
	}
	if req.ValidateOnly {
		t.Fatal("expected a real run, not validate-only")
	}
	overrides := req.GetOverrides().GetContainerOverrides()
	if len(overrides) != 1 {
		t.Fatalf("container overrides: %d", len(overrides))
	}
	env := overrides[0].GetEnv()
	if len(env) != 1 {
		t.Fatalf("env vars: %d", len(env))
	}
	if env[0].GetName() != "EPISODE_ID" {
		t.Fatalf("env name %q", env[0].GetName())
	}
	if env[0].GetValue() != "ep_01ja2b3c4d5e6f" {
		t.Fatalf("env value %q", env[0].GetValue())
	}
	if env[0].GetValueSource() != nil {
		t.Fatal("EPISODE_ID must be a literal, not a secret source")
	}
}

func TestCloudRunEnqueueTrimsEpisodeID(t *testing.T) {
	fake := &fakeCloudRunClient{}
	d := newCloudRunDispatcher("projects/p/locations/r/jobs/j", fake)
	if err := d.Enqueue(context.Background(), "  ep_trim  "); err != nil {
		t.Fatal(err)
	}
	if got := fake.last().GetOverrides().GetContainerOverrides()[0].GetEnv()[0].GetValue(); got != "ep_trim" {
		t.Fatalf("trimmed value %q", got)
	}
}

func TestCloudRunEnqueueRejectsEmptyID(t *testing.T) {
	fake := &fakeCloudRunClient{}
	d := newCloudRunDispatcher("projects/p/locations/r/jobs/j", fake)
	for _, id := range []string{"", "   "} {
		err := d.Enqueue(context.Background(), id)
		if !errors.Is(err, ErrEpisodeIDRequired) {
			t.Fatalf("id %q: got %v, want ErrEpisodeIDRequired", id, err)
		}
	}
	if fake.last() != nil {
		t.Fatal("API must not be called for an empty episode id")
	}
}

func TestCloudRunEnqueueWrapsAPIError(t *testing.T) {
	want := errors.New("quota exceeded")
	fake := &fakeCloudRunClient{err: want}
	d := newCloudRunDispatcher("projects/p/locations/r/jobs/j", fake)

	err := d.Enqueue(context.Background(), "ep_1")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want wrapped quota error", err)
	}
	if !strings.Contains(err.Error(), "run cloud run job") {
		t.Fatalf("error should name the operation: %v", err)
	}
}

func TestCloudRunEnqueuePropagatesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fake := &fakeCloudRunClient{
		run: func(ctx context.Context, req *runpb.RunJobRequest) error {
			return ctx.Err()
		},
	}
	d := newCloudRunDispatcher("projects/p/locations/r/jobs/j", fake)
	err := d.Enqueue(ctx, "ep_1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestCloudRunClose(t *testing.T) {
	fake := &fakeCloudRunClient{}
	d := newCloudRunDispatcher("projects/p/locations/r/jobs/j", fake)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if !fake.closed {
		t.Fatal("expected client Close")
	}
	var nild *CloudRunDispatcher
	if err := nild.Close(); err != nil {
		t.Fatalf("nil dispatcher Close: %v", err)
	}
}

func TestCloudRunEnqueueIsFireAndForget(t *testing.T) {
	// A successful RunJob RPC is enough; the dispatcher must not wait on the
	// Cloud Run Job execution itself (that is the worker's job).
	started := make(chan struct{})
	fake := &fakeCloudRunClient{
		run: func(ctx context.Context, req *runpb.RunJobRequest) error {
			close(started)
			return nil
		},
	}
	d := newCloudRunDispatcher("projects/p/locations/r/jobs/j", fake)
	if err := d.Enqueue(context.Background(), "ep_1"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	default:
		t.Fatal("RunJob was not invoked")
	}
}
