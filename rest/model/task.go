package model

import (
	"fmt"
	"time"

	"github.com/evergreen-ci/evergreen"
	"github.com/evergreen-ci/evergreen/apimodels"
	"github.com/evergreen-ci/evergreen/model"
	"github.com/evergreen-ci/evergreen/model/artifact"
	"github.com/evergreen-ci/evergreen/model/host"
	"github.com/evergreen-ci/evergreen/model/task"
	"github.com/evergreen-ci/utility"
	"github.com/mongodb/grip"
	"github.com/pkg/errors"
)

const (
	TaskLogLinkFormat  = "%s/task_log_raw/%s/%d?type=%s"
	EventLogLinkFormat = "%s/event_log/task/%s"
)

// APITask is the model to be returned by the API whenever tasks are fetched.
type APITask struct {
	Id                      *string             `json:"task_id"`
	ProjectId               *string             `json:"project_id"`
	ProjectIdentifier       *string             `json:"project_identifier"`
	CreateTime              *time.Time          `json:"create_time"`
	DispatchTime            *time.Time          `json:"dispatch_time"`
	ScheduledTime           *time.Time          `json:"scheduled_time"`
	ContainerAllocatedTime  *time.Time          `json:"container_allocated_time"`
	StartTime               *time.Time          `json:"start_time"`
	FinishTime              *time.Time          `json:"finish_time"`
	IngestTime              *time.Time          `json:"ingest_time"`
	ActivatedTime           *time.Time          `json:"activated_time"`
	Version                 *string             `json:"version_id"`
	Revision                *string             `json:"revision"`
	Priority                int64               `json:"priority"`
	Activated               bool                `json:"activated"`
	ActivatedBy             *string             `json:"activated_by"`
	BuildId                 *string             `json:"build_id"`
	DistroId                *string             `json:"distro_id"`
	Container               *string             `json:"container"`
	BuildVariant            *string             `json:"build_variant"`
	BuildVariantDisplayName *string             `json:"build_variant_display_name"`
	DependsOn               []APIDependency     `json:"depends_on"`
	DisplayName             *string             `json:"display_name"`
	HostId                  *string             `json:"host_id"`
	Execution               int                 `json:"execution"`
	Order                   int                 `json:"order"`
	Status                  *string             `json:"status"`
	DisplayStatus           *string             `json:"display_status"`
	Details                 ApiTaskEndDetail    `json:"status_details"`
	Logs                    LogLinks            `json:"logs"`
	TimeTaken               APIDuration         `json:"time_taken_ms"`
	ExpectedDuration        APIDuration         `json:"expected_duration_ms"`
	EstimatedStart          APIDuration         `json:"est_wait_to_start_ms"`
	PreviousExecutions      []APITask           `json:"previous_executions,omitempty"`
	GenerateTask            bool                `json:"generate_task"`
	GeneratedBy             string              `json:"generated_by"`
	Artifacts               []APIFile           `json:"artifacts"`
	DisplayOnly             bool                `json:"display_only"`
	ParentTaskId            string              `json:"parent_task_id"`
	ExecutionTasks          []*string           `json:"execution_tasks,omitempty"`
	Tags                    []*string           `json:"tags,omitempty"`
	Mainline                bool                `json:"mainline"`
	TaskGroup               string              `json:"task_group,omitempty"`
	TaskGroupMaxHosts       int                 `json:"task_group_max_hosts,omitempty"`
	Blocked                 bool                `json:"blocked"`
	Requester               *string             `json:"requester"`
	TestResults             []APITest           `json:"test_results"`
	Aborted                 bool                `json:"aborted"`
	AbortInfo               APIAbortInfo        `json:"abort_info,omitempty"`
	CanSync                 bool                `json:"can_sync,omitempty"`
	SyncAtEndOpts           APISyncAtEndOptions `json:"sync_at_end_opts"`
	AMI                     *string             `json:"ami"`
	MustHaveResults         bool                `json:"must_have_test_results"`
	BaseTask                APIBaseTaskInfo     `json:"base_task"`
	// These fields are used by graphql gen, but do not need to be exposed
	// via Evergreen's user-facing API.
	OverrideDependencies bool `json:"-"`
	Archived             bool `json:"archived"`
	HasCedarResults      bool `json:"-"`
	CedarResultsFailed   bool `json:"-"`
}

type APIAbortInfo struct {
	User       string `json:"user,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	NewVersion string `json:"new_version,omitempty"`
	PRClosed   bool   `json:"pr_closed,omitempty"`
}

type LogLinks struct {
	AllLogLink    *string `json:"all_log"`
	TaskLogLink   *string `json:"task_log"`
	AgentLogLink  *string `json:"agent_log"`
	SystemLogLink *string `json:"system_log"`
	EventLogLink  *string `json:"event_log"`
}

type ApiTaskEndDetail struct {
	Status      *string           `json:"status"`
	Type        *string           `json:"type"`
	Description *string           `json:"desc"`
	TimedOut    bool              `json:"timed_out"`
	TimeoutType *string           `json:"timeout_type"`
	OOMTracker  APIOomTrackerInfo `json:"oom_tracker_info"`
}

func (at *ApiTaskEndDetail) BuildFromService(t interface{}) error {
	v, ok := t.(apimodels.TaskEndDetail)
	if !ok {
		return errors.Errorf("programmatic error: expected task end detail but got type %T", t)
	}
	at.Status = utility.ToStringPtr(v.Status)
	at.Type = utility.ToStringPtr(v.Type)
	at.Description = utility.ToStringPtr(v.Description)
	at.TimedOut = v.TimedOut
	at.TimeoutType = utility.ToStringPtr(v.TimeoutType)

	apiOomTracker := APIOomTrackerInfo{}
	if err := apiOomTracker.BuildFromService(v.OOMTracker); err != nil {
		return errors.Wrap(err, "converting OOM tracker info to API model")
	}
	at.OOMTracker = apiOomTracker

	return nil
}

func (ad *ApiTaskEndDetail) ToService() (interface{}, error) {
	detail := apimodels.TaskEndDetail{
		Status:      utility.FromStringPtr(ad.Status),
		Type:        utility.FromStringPtr(ad.Type),
		Description: utility.FromStringPtr(ad.Description),
		TimedOut:    ad.TimedOut,
		TimeoutType: utility.FromStringPtr(ad.TimeoutType),
	}
	oomTrackerIface, err := ad.OOMTracker.ToService()
	if err != nil {
		return nil, errors.Wrap(err, "converting OOM tracker info to service model")
	}
	detail.OOMTracker = oomTrackerIface.(*apimodels.OOMTrackerInfo)

	return detail, nil
}

type APIOomTrackerInfo struct {
	Detected bool  `json:"detected"`
	Pids     []int `json:"pids"`
}

func (at *APIOomTrackerInfo) BuildFromService(t interface{}) error {
	v, ok := t.(*apimodels.OOMTrackerInfo)
	if !ok {
		return errors.Errorf("programmatic error: expected OOM tracker info but got type %T", t)
	}
	if v != nil {
		at.Detected = v.Detected
		at.Pids = v.Pids
	}

	return nil
}

func (ad *APIOomTrackerInfo) ToService() (interface{}, error) {
	return &apimodels.OOMTrackerInfo{
		Detected: ad.Detected,
		Pids:     ad.Pids,
	}, nil
}

func (at *APITask) BuildPreviousExecutions(tasks []task.Task, url string) error {
	at.PreviousExecutions = make([]APITask, len(tasks))
	for i := range at.PreviousExecutions {
		if err := at.PreviousExecutions[i].BuildFromArgs(&tasks[i], &APITaskArgs{
			IncludeProjectIdentifier: true,
			IncludeAMI:               true,
			IncludeArtifacts:         true,
			LogURL:                   url,
		}); err != nil {
			return errors.Wrapf(err, "converting previous task execution at index %d to API model", i)
		}
	}

	return nil
}

// Deprecated: BuildFromArgs should be used instead to add fields that aren't from the task collection
//
// BuildFromService converts from a service level task by loading the data
// into the appropriate fields of the APITask.
func (at *APITask) BuildFromService(t interface{}) error {
	switch v := t.(type) {
	case *task.Task:
		id := v.Id
		// Old tasks are stored in a separate collection with ID set to
		// "old_task_ID" + "_" + "execution_number". This ID is not exposed to the user,
		// however. Instead in the UI executions are represented with a "/" and could be
		// represented in other ways elsewhere. The correct way to represent an old task is
		// with the same ID as the last execution, since semantically the tasks differ in
		// their execution number, not in their ID.
		if v.OldTaskId != "" {
			id = v.OldTaskId
		}
		(*at) = APITask{
			Id:                      utility.ToStringPtr(id),
			ProjectId:               utility.ToStringPtr(v.Project),
			CreateTime:              ToTimePtr(v.CreateTime),
			DispatchTime:            ToTimePtr(v.DispatchTime),
			ScheduledTime:           ToTimePtr(v.ScheduledTime),
			ContainerAllocatedTime:  ToTimePtr(v.ContainerAllocatedTime),
			StartTime:               ToTimePtr(v.StartTime),
			FinishTime:              ToTimePtr(v.FinishTime),
			IngestTime:              ToTimePtr(v.IngestTime),
			ActivatedTime:           ToTimePtr(v.ActivatedTime),
			Version:                 utility.ToStringPtr(v.Version),
			Revision:                utility.ToStringPtr(v.Revision),
			Priority:                v.Priority,
			Activated:               v.Activated,
			ActivatedBy:             utility.ToStringPtr(v.ActivatedBy),
			BuildId:                 utility.ToStringPtr(v.BuildId),
			DistroId:                utility.ToStringPtr(v.DistroId),
			Container:               utility.ToStringPtr(v.Container),
			BuildVariant:            utility.ToStringPtr(v.BuildVariant),
			BuildVariantDisplayName: utility.ToStringPtr(v.BuildVariantDisplayName),
			DisplayName:             utility.ToStringPtr(v.DisplayName),
			HostId:                  utility.ToStringPtr(v.HostId),
			Tags:                    utility.ToStringPtrSlice(v.Tags),
			Execution:               v.Execution,
			Order:                   v.RevisionOrderNumber,
			Status:                  utility.ToStringPtr(v.Status),
			DisplayStatus:           utility.ToStringPtr(v.GetDisplayStatus()),
			ExpectedDuration:        NewAPIDuration(v.ExpectedDuration),
			GenerateTask:            v.GenerateTask,
			GeneratedBy:             v.GeneratedBy,
			DisplayOnly:             v.DisplayOnly,
			Mainline:                (v.Requester == evergreen.RepotrackerVersionRequester),
			TaskGroup:               v.TaskGroup,
			TaskGroupMaxHosts:       v.TaskGroupMaxHosts,
			Blocked:                 v.Blocked(),
			Requester:               utility.ToStringPtr(v.Requester),
			Aborted:                 v.Aborted,
			CanSync:                 v.CanSync,
			HasCedarResults:         v.HasCedarResults,
			CedarResultsFailed:      v.CedarResultsFailed,
			MustHaveResults:         v.MustHaveResults,
			ParentTaskId:            utility.FromStringPtr(v.DisplayTaskId),
			SyncAtEndOpts: APISyncAtEndOptions{
				Enabled:  v.SyncAtEndOpts.Enabled,
				Statuses: v.SyncAtEndOpts.Statuses,
				Timeout:  v.SyncAtEndOpts.Timeout,
			},
			AbortInfo: APIAbortInfo{
				NewVersion: v.AbortInfo.NewVersion,
				TaskID:     v.AbortInfo.TaskID,
				User:       v.AbortInfo.User,
				PRClosed:   v.AbortInfo.PRClosed,
			},
		}
		if v.BaseTask.Id != "" {
			at.BaseTask = APIBaseTaskInfo{
				Id:     utility.ToStringPtr(v.BaseTask.Id),
				Status: utility.ToStringPtr(v.BaseTask.Status),
			}
		}

		if v.TimeTaken != 0 {
			at.TimeTaken = NewAPIDuration(v.TimeTaken)
		} else if v.Status == evergreen.TaskStarted {
			at.TimeTaken = NewAPIDuration(time.Since(v.StartTime))
		}

		if v.ParentPatchID != "" {
			at.Version = utility.ToStringPtr(v.ParentPatchID)
			if v.ParentPatchNumber != 0 {
				at.Order = v.ParentPatchNumber
			}
		}

		if err := at.Details.BuildFromService(v.Details); err != nil {
			return errors.Wrap(err, "converting task end details to API model")
		}

		if len(v.ExecutionTasks) > 0 {
			ets := []*string{}
			for _, t := range v.ExecutionTasks {
				ets = append(ets, utility.ToStringPtr(t))
			}
			at.ExecutionTasks = ets
		}

		if len(v.DependsOn) > 0 {
			dependsOn := make([]APIDependency, len(v.DependsOn))
			for i, dep := range v.DependsOn {
				apiDep := APIDependency{}
				apiDep.BuildFromService(dep)
				dependsOn[i] = apiDep
			}
			at.DependsOn = dependsOn
		}

		at.OverrideDependencies = v.OverrideDependencies
		at.Archived = v.Archived
	default:
		return errors.New(fmt.Sprintf("Incorrect type %T when unmarshalling task", t))
	}

	return nil
}

type APITaskArgs struct {
	IncludeProjectIdentifier bool
	IncludeAMI               bool
	IncludeArtifacts         bool
	LogURL                   string
}

// BuildFromArgs converts from a service level task by loading the data
// into the appropriate fields of the APITask. It takes optional arguments to populate
// additional fields.
func (at *APITask) BuildFromArgs(t interface{}, args *APITaskArgs) error {
	err := at.BuildFromService(t)
	if err != nil {
		return err
	}
	if args == nil {
		return nil
	}
	if args.LogURL != "" {
		ll := LogLinks{
			AllLogLink:    utility.ToStringPtr(fmt.Sprintf(TaskLogLinkFormat, args.LogURL, utility.FromStringPtr(at.Id), at.Execution, "ALL")),
			TaskLogLink:   utility.ToStringPtr(fmt.Sprintf(TaskLogLinkFormat, args.LogURL, utility.FromStringPtr(at.Id), at.Execution, "T")),
			AgentLogLink:  utility.ToStringPtr(fmt.Sprintf(TaskLogLinkFormat, args.LogURL, utility.FromStringPtr(at.Id), at.Execution, "E")),
			SystemLogLink: utility.ToStringPtr(fmt.Sprintf(TaskLogLinkFormat, args.LogURL, utility.FromStringPtr(at.Id), at.Execution, "S")),
			EventLogLink:  utility.ToStringPtr(fmt.Sprintf(EventLogLinkFormat, args.LogURL, utility.FromStringPtr(at.Id))),
		}
		at.Logs = ll
	}
	if args.IncludeAMI {
		if err := at.GetAMI(); err != nil {
			return errors.Wrap(err, "getting AMI")
		}
	}
	if args.IncludeArtifacts {
		if err := at.GetArtifacts(); err != nil {
			return errors.Wrap(err, "getting artifacts")
		}
	}
	if args.IncludeProjectIdentifier {
		at.GetProjectIdentifier()
	}

	return nil
}

func (at *APITask) GetAMI() error {
	if at.AMI != nil {
		return nil
	}
	if utility.FromStringPtr(at.HostId) != "" {
		h, err := host.FindOneId(utility.FromStringPtr(at.HostId))
		if err != nil {
			return errors.Wrapf(err, "finding host '%s' for task", utility.FromStringPtr(at.HostId))
		}
		if h != nil {
			ami := h.GetAMI()
			if ami != "" {
				at.AMI = utility.ToStringPtr(ami)
			}
		}
	}
	return nil
}

func (at *APITask) GetProjectIdentifier() {
	if at.ProjectIdentifier != nil {
		return
	}
	if utility.FromStringPtr(at.ProjectId) != "" {
		identifier, err := model.GetIdentifierForProject(utility.FromStringPtr(at.ProjectId))
		if err == nil {
			at.ProjectIdentifier = utility.ToStringPtr(identifier)
		}
	}
}

// ToService returns a service layer task using the data from the APITask.
func (ad *APITask) ToService() (interface{}, error) {
	st := &task.Task{
		Id:                      utility.FromStringPtr(ad.Id),
		Project:                 utility.FromStringPtr(ad.ProjectId),
		Version:                 utility.FromStringPtr(ad.Version),
		Revision:                utility.FromStringPtr(ad.Revision),
		Priority:                ad.Priority,
		Activated:               ad.Activated,
		ActivatedBy:             utility.FromStringPtr(ad.ActivatedBy),
		BuildId:                 utility.FromStringPtr(ad.BuildId),
		DistroId:                utility.FromStringPtr(ad.DistroId),
		Container:               utility.FromStringPtr(ad.Container),
		BuildVariant:            utility.FromStringPtr(ad.BuildVariant),
		BuildVariantDisplayName: utility.FromStringPtr(ad.BuildVariantDisplayName),
		DisplayName:             utility.FromStringPtr(ad.DisplayName),
		HostId:                  utility.FromStringPtr(ad.HostId),
		Execution:               ad.Execution,
		RevisionOrderNumber:     ad.Order,
		Status:                  utility.FromStringPtr(ad.Status),
		DisplayStatus:           utility.FromStringPtr(ad.DisplayStatus),
		TimeTaken:               ad.TimeTaken.ToDuration(),
		ExpectedDuration:        ad.ExpectedDuration.ToDuration(),
		GenerateTask:            ad.GenerateTask,
		GeneratedBy:             ad.GeneratedBy,
		DisplayOnly:             ad.DisplayOnly,
		Requester:               utility.FromStringPtr(ad.Requester),
		CanSync:                 ad.CanSync,
		HasCedarResults:         ad.HasCedarResults,
		CedarResultsFailed:      ad.CedarResultsFailed,
		MustHaveResults:         ad.MustHaveResults,
		SyncAtEndOpts: task.SyncAtEndOptions{
			Enabled:  ad.SyncAtEndOpts.Enabled,
			Statuses: ad.SyncAtEndOpts.Statuses,
			Timeout:  ad.SyncAtEndOpts.Timeout,
		},
		BaseTask: task.BaseTaskInfo{
			Id:     utility.FromStringPtr(ad.BaseTask.Id),
			Status: utility.FromStringPtr(ad.BaseTask.Status),
		},
		DisplayTaskId: utility.ToStringPtr(ad.ParentTaskId),
		Aborted:       ad.Aborted,
	}
	catcher := grip.NewBasicCatcher()
	serviceDetails, err := ad.Details.ToService()
	catcher.Add(err)
	st.Details = serviceDetails.(apimodels.TaskEndDetail)
	createTime, err := FromTimePtr(ad.CreateTime)
	catcher.Add(err)
	dispatchTime, err := FromTimePtr(ad.DispatchTime)
	catcher.Add(err)
	scheduledTime, err := FromTimePtr(ad.ScheduledTime)
	catcher.Add(err)
	containerAllocatedTime, err := FromTimePtr(ad.ContainerAllocatedTime)
	catcher.Add(err)
	startTime, err := FromTimePtr(ad.StartTime)
	catcher.Add(err)
	finishTime, err := FromTimePtr(ad.FinishTime)
	catcher.Add(err)
	ingestTime, err := FromTimePtr(ad.IngestTime)
	catcher.Add(err)
	activatedTime, err := FromTimePtr(ad.ActivatedTime)
	catcher.Add(err)
	if catcher.HasErrors() {
		return nil, catcher.Resolve()
	}

	st.CreateTime = createTime
	st.DispatchTime = dispatchTime
	st.ScheduledTime = scheduledTime
	st.ContainerAllocatedTime = containerAllocatedTime
	st.StartTime = startTime
	st.FinishTime = finishTime
	st.IngestTime = ingestTime
	st.ActivatedTime = activatedTime
	if len(ad.ExecutionTasks) > 0 {
		ets := []string{}
		for _, t := range ad.ExecutionTasks {
			ets = append(ets, utility.FromStringPtr(t))
		}
		st.ExecutionTasks = ets
	}

	dependsOn := make([]task.Dependency, len(ad.DependsOn))

	for i, dep := range ad.DependsOn {
		dependsOn[i].TaskId = dep.TaskId
		dependsOn[i].Status = dep.Status
	}

	st.DependsOn = dependsOn
	st.OverrideDependencies = ad.OverrideDependencies
	st.Archived = ad.Archived
	return interface{}(st), nil
}

func (at *APITask) GetArtifacts() error {
	var err error
	var entries []artifact.Entry
	if at.DisplayOnly {
		ets := []artifact.TaskIDAndExecution{}
		for _, t := range at.ExecutionTasks {
			ets = append(ets, artifact.TaskIDAndExecution{TaskID: *t, Execution: at.Execution})
		}
		if len(ets) > 0 {
			entries, err = artifact.FindAll(artifact.ByTaskIdsAndExecutions(ets))
		}
	} else {
		entries, err = artifact.FindAll(artifact.ByTaskIdAndExecution(utility.FromStringPtr(at.Id), at.Execution))
	}
	if err != nil {
		return errors.Wrap(err, "retrieving artifacts")
	}
	for _, entry := range entries {
		var strippedFiles []artifact.File
		// The route requires a user, so hasUser is always true.
		strippedFiles, err = artifact.StripHiddenFiles(entry.Files, true)
		if err != nil {
			return err
		}
		for _, file := range strippedFiles {
			apiFile := APIFile{}
			err := apiFile.BuildFromService(file)
			if err != nil {
				return err
			}
			at.Artifacts = append(at.Artifacts, apiFile)
		}
	}

	return nil
}

type APISyncAtEndOptions struct {
	Enabled  bool          `json:"enabled"`
	Statuses []string      `json:"statuses"`
	Timeout  time.Duration `json:"timeout"`
}

type APIDependency struct {
	TaskId string `bson:"_id" json:"id"`
	Status string `bson:"status" json:"status"`
}

func (ad *APIDependency) BuildFromService(dep task.Dependency) {
	ad.TaskId = dep.TaskId
	ad.Status = dep.Status
}

func (ad *APIDependency) ToService() (interface{}, error) {
	return nil, errors.New("ToService() is not implemented for APIDependency")
}
