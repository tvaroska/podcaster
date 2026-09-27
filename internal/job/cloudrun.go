package job

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	run "cloud.google.com/go/run/apiv2"
	runpb "cloud.google.com/go/run/apiv2/runpb"
)

var (
	ErrEpisodeIDRequired  = errors.New("episode id is required")
	ErrInvalidJobIdentity = errors.New("cloud run job requires project, region, and job name")
)

// cloudRunJobClient is the subset of the Cloud Run Jobs API the dispatcher uses.
// The real GAPIC client is adapted in production; tests inject a fake.
type cloudRunJobClient interface {
	RunJob(ctx context.Context, req *runpb.RunJobRequest) error
	Close() error
}

type gcpJobsClient struct {
	client *run.JobsClient
}

func (c *gcpJobsClient) RunJob(ctx context.Context, req *runpb.RunJobRequest) error {
	// RunJob returns a long-running operation. We fire-and-forget: the worker
	// claims the episode via CAS. Waiting here would block ingest on TTS.
	_, err := c.client.RunJob(ctx, req)
	return err
}

func (c *gcpJobsClient) Close() error {
	return c.client.Close()
}

// CloudRunDispatcher triggers a Cloud Run Job execution with EPISODE_ID set.
type CloudRunDispatcher struct {
	client cloudRunJobClient
	name   string // projects/{project}/locations/{region}/jobs/{job}
}

var (
	_ Dispatcher = (*CloudRunDispatcher)(nil)
	_ io.Closer  = (*CloudRunDispatcher)(nil)
)

func NewCloudRun(ctx context.Context, project, region, jobName string) (*CloudRunDispatcher, error) {
	name, err := cloudRunJobName(project, region, jobName)
	if err != nil {
		return nil, err
	}
	client, err := run.NewJobsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("cloud run jobs client: %w", err)
	}
	return newCloudRunDispatcher(name, &gcpJobsClient{client: client}), nil
}

func newCloudRunDispatcher(name string, client cloudRunJobClient) *CloudRunDispatcher {
	return &CloudRunDispatcher{client: client, name: name}
}

func cloudRunJobName(project, region, jobName string) (string, error) {
	project = strings.TrimSpace(project)
	region = strings.TrimSpace(region)
	jobName = strings.TrimSpace(jobName)
	if project == "" || region == "" || jobName == "" {
		return "", ErrInvalidJobIdentity
	}
	if strings.Contains(jobName, "/") {
		return "", fmt.Errorf("%w: CLOUD_RUN_JOB_NAME must be a job name, not a resource path", ErrInvalidJobIdentity)
	}
	return fmt.Sprintf("projects/%s/locations/%s/jobs/%s", project, region, jobName), nil
}

func (d *CloudRunDispatcher) Enqueue(ctx context.Context, episodeID string) error {
	episodeID = strings.TrimSpace(episodeID)
	if episodeID == "" {
		return ErrEpisodeIDRequired
	}
	err := d.client.RunJob(ctx, &runpb.RunJobRequest{
		Name: d.name,
		Overrides: &runpb.RunJobRequest_Overrides{
			ContainerOverrides: []*runpb.RunJobRequest_Overrides_ContainerOverride{{
				Env: []*runpb.EnvVar{{
					Name:   "EPISODE_ID",
					Values: &runpb.EnvVar_Value{Value: episodeID},
				}},
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("run cloud run job: %w", err)
	}
	return nil
}

func (d *CloudRunDispatcher) Close() error {
	if d == nil || d.client == nil {
		return nil
	}
	return d.client.Close()
}
