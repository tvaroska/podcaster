package job

import (
	"context"
	"fmt"

	run "cloud.google.com/go/run/apiv2"
	runpb "cloud.google.com/go/run/apiv2/runpb"
)

// CloudRunDispatcher triggers a Cloud Run Job execution with EPISODE_ID set.
type CloudRunDispatcher struct {
	client *run.JobsClient
	name   string // projects/{project}/locations/{region}/jobs/{job}
}

func NewCloudRun(ctx context.Context, project, region, job string) (*CloudRunDispatcher, error) {
	client, err := run.NewJobsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("cloud run jobs client: %w", err)
	}
	name := fmt.Sprintf("projects/%s/locations/%s/jobs/%s", project, region, job)
	return &CloudRunDispatcher{client: client, name: name}, nil
}

func (d *CloudRunDispatcher) Enqueue(ctx context.Context, episodeID string) error {
	_, err := d.client.RunJob(ctx, &runpb.RunJobRequest{
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
	return d.client.Close()
}
