package units

import (
	"context"
	"fmt"
	"time"

	"github.com/evergreen-ci/evergreen/model"
	"github.com/mongodb/amboy"
	"github.com/mongodb/amboy/job"
	"github.com/mongodb/amboy/registry"
	"github.com/mongodb/grip"
	"github.com/pkg/errors"
)

const (
	latencyStatsCollectorJobName  = "latency-stats-collector"
	latencyStatsCollectorInterval = time.Minute
)

func init() {
	registry.AddJobType(latencyStatsCollectorJobName,
		func() amboy.Job { return makeLatencyStatsCollector() })
}

type latencyStatsCollector struct {
	job.Base `bson:"job_base" json:"job_base" yaml:"job_base"`
	Duration time.Duration `bson:"dur" json:"duration" yaml:"duration"`
}

// NewLatencyStatsCollector captures a single report of the latency of
// tasks that have started in the last minute.
func NewLatencyStatsCollector(id string, duration time.Duration) amboy.Job {
	t := makeLatencyStatsCollector()
	t.SetID(fmt.Sprintf("%s-%s", latencyStatsCollectorJobName, id))
	t.Duration = duration
	return t
}

func makeLatencyStatsCollector() *latencyStatsCollector {
	j := &latencyStatsCollector{
		Base: job.Base{
			JobType: amboy.JobType{
				Name:    latencyStatsCollectorJobName,
				Version: 0,
			},
		},
		Duration: latencyStatsCollectorInterval,
	}
	return j
}

func (j *latencyStatsCollector) Run(_ context.Context) {
	defer j.MarkComplete()

	latencies, err := model.AverageHostTaskLatency(j.Duration)
	if err != nil {
		j.AddError(errors.Wrap(err, "error finding task latencies"))
		return
	}
	grip.Info(latencies)
}
