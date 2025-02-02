package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/evergreen-ci/evergreen"
	"github.com/evergreen-ci/evergreen/apimodels"
	"github.com/evergreen-ci/evergreen/model"
	"github.com/evergreen-ci/evergreen/model/artifact"
	"github.com/evergreen-ci/evergreen/model/host"
	"github.com/evergreen-ci/evergreen/model/patch"
	"github.com/evergreen-ci/evergreen/model/task"
	"github.com/evergreen-ci/evergreen/rest/route"
	"github.com/evergreen-ci/evergreen/util"
	"github.com/evergreen-ci/evergreen/validator"
	"github.com/evergreen-ci/gimlet"
	"github.com/evergreen-ci/utility"
	"github.com/mongodb/amboy"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
)

const (
	APIServerLockTitle = evergreen.APIServerTaskActivator
	TaskStartCaller    = "start task"
	EndTaskCaller      = "end task"
)

// APIServer handles communication with Evergreen agents and other back-end requests.
type APIServer struct {
	UserManager         gimlet.UserManager
	Settings            evergreen.Settings
	env                 evergreen.Environment
	queue               amboy.Queue
	taskDispatcher      model.TaskQueueItemDispatcher
	taskAliasDispatcher model.TaskQueueItemDispatcher
}

// NewAPIServer returns an APIServer initialized with the given settings and plugins.
func NewAPIServer(env evergreen.Environment, queue amboy.Queue) (*APIServer, error) {
	settings := env.Settings()

	if err := settings.Validate(); err != nil {
		return nil, errors.WithStack(err)
	}

	as := &APIServer{
		UserManager:         env.UserManager(),
		Settings:            *settings,
		env:                 env,
		queue:               queue,
		taskDispatcher:      model.NewTaskDispatchService(taskDispatcherTTL),
		taskAliasDispatcher: model.NewTaskDispatchAliasService(taskDispatcherTTL),
	}

	return as, nil
}

// MustHaveTask gets the task from an HTTP Request.
// Panics if the task is not in request context.
func MustHaveTask(r *http.Request) *task.Task {
	t := GetTask(r)
	if t == nil {
		panic("no task attached to request")
	}
	return t
}

// MustHaveHost gets the host from the HTTP Request
// Panics if the host is not in the request context
func MustHaveHost(r *http.Request) *host.Host {
	h := GetHost(r)
	if h == nil {
		panic("no host attached to request")
	}
	return h
}

// MustHaveProject gets the project from the HTTP request and panics
// if there is no project specified
func MustHaveProject(r *http.Request) *model.Project {
	p := GetProject(r)
	if p == nil {
		panic("no project attached to request")
	}
	return p
}

// requireTask get the task from the request header and ensures that there is a task. It checks the secret
// in the header with the secret in the db to ensure that they are the same.
func (as *APIServer) requireTask(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, code, err := model.ValidateTask(gimlet.GetVars(r)["taskId"], false, r)
		if err != nil {
			as.LoggedError(w, r, code, errors.Wrap(err, "invalid task"))
			return
		}
		r = setAPITaskContext(r, t)
		next(w, r)
	}
}

func (as *APIServer) requireTaskStrict(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, code, err := model.ValidateTask(gimlet.GetVars(r)["taskId"], true, r)
		if err != nil {
			as.LoggedError(w, r, code, errors.Wrap(err, "invalid task"))
			return
		}
		r = setAPITaskContext(r, t)
		next(w, r)
	}
}

// requireProject finds the projectId in the request and adds the
// project and project ref to the request context.
func (as *APIServer) requireProject(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectId := gimlet.GetVars(r)["projectId"]
		if projectId == "" {
			as.LoggedError(w, r, http.StatusBadRequest, errors.New("missing project Id"))
			return
		}

		projectRef, err := model.FindBranchProjectRef(projectId)
		if err != nil {
			as.LoggedError(w, r, http.StatusInternalServerError, err)
		}
		if projectRef == nil {
			as.LoggedError(w, r, http.StatusNotFound, errors.New("project not found"))
			return
		}

		_, p, err := model.FindLatestVersionWithValidProject(projectRef.Id)
		if err != nil {
			as.LoggedError(w, r, http.StatusInternalServerError,
				errors.Wrap(err, "Error getting patch"))
			return
		}
		if p == nil {
			as.LoggedError(w, r, http.StatusNotFound,
				errors.Errorf("can't find config for : %s", projectRef.Id))
			return
		}

		r = setProjectContext(r, p)

		next(w, r)
	}
}

func (as *APIServer) requireHost(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, code, err := model.ValidateHost(gimlet.GetVars(r)["hostId"], r)
		if err != nil {
			as.LoggedError(w, r, code, errors.Wrap(err, "host not assigned to run task"))
			return
		}
		// update host access time
		if err := h.UpdateLastCommunicated(); err != nil {
			grip.Warningf("Could not update host last communication time for %s: %+v", h.Id, err)
		}
		// Since the host has contacted the app server, we should prevent the
		// app server from attempting to deploy agents or agent monitors.
		// Deciding whether or not we should redeploy agents or agent monitors
		// is handled within the REST route handler.
		if h.NeedsNewAgent {
			grip.Warning(message.WrapError(h.SetNeedsNewAgent(false), "problem clearing host needs new agent"))
		}
		if h.NeedsNewAgentMonitor {
			grip.Warning(message.WrapError(h.SetNeedsNewAgentMonitor(false), "problem clearing host needs new agent monitor"))
		}
		r = setAPIHostContext(r, h)
		next(w, r)
	}
}

func (as *APIServer) GetParserProject(w http.ResponseWriter, r *http.Request) {
	t := MustHaveTask(r)
	v, err := model.VersionFindOne(model.VersionById(t.Version))
	if err != nil {
		as.LoggedError(w, r, http.StatusInternalServerError, err)
		return
	}

	if v == nil {
		http.Error(w, "version not found", http.StatusNotFound)
		return
	}
	pp, err := model.ParserProjectFindOneById(t.Version)
	if err != nil {
		as.LoggedError(w, r, http.StatusInternalServerError, err)
		return
	}
	// handle legacy
	if pp == nil || pp.ConfigUpdateNumber < v.ConfigUpdateNumber {
		pp = &model.ParserProject{}
		if err = util.UnmarshalYAMLWithFallback([]byte(v.Config), pp); err != nil {
			http.Error(w, "invalid version config", http.StatusNotFound)
			return
		}
	}
	if pp.Functions == nil {
		pp.Functions = map[string]*model.YAMLCommandSet{}
	}
	projBytes, err := bson.Marshal(pp)
	if err != nil {
		as.LoggedError(w, r, http.StatusInternalServerError, err)
		gimlet.WriteJSONResponse(w, http.StatusInternalServerError, responseError{Message: "problem marshalling to bson"})
		return
	}
	gimlet.WriteBinary(w, projBytes)
}

func (as *APIServer) GetProjectRef(w http.ResponseWriter, r *http.Request) {
	t := MustHaveTask(r)

	p, err := model.FindMergedProjectRef(t.Project, t.Version, true)
	if err != nil {
		as.LoggedError(w, r, http.StatusInternalServerError, err)
		return
	}

	if p == nil {
		http.Error(w, "project ref not found", http.StatusNotFound)
		return
	}

	gimlet.WriteJSON(w, p)
}

func (as *APIServer) GetExpansions(w http.ResponseWriter, r *http.Request) {
	t := MustHaveTask(r)
	h := MustHaveHost(r)
	settings := as.GetSettings()
	oauthToken, err := settings.GetGithubOauthToken()
	if err != nil {
		as.LoggedError(w, r, http.StatusInternalServerError, err)
	}

	e, err := model.PopulateExpansions(t, h, oauthToken)
	if err != nil {
		as.LoggedError(w, r, http.StatusInternalServerError, err)
		return
	}

	gimlet.WriteJSON(w, e)
}

// AttachTestLog is the API Server hook for getting
// the test logs and storing them in the test_logs collection.
func (as *APIServer) AttachTestLog(w http.ResponseWriter, r *http.Request) {
	if as.GetSettings().ServiceFlags.TaskLoggingDisabled {
		http.Error(w, "task logging is disabled", http.StatusConflict)
		return
	}
	t := MustHaveTask(r)
	log := &model.TestLog{}
	err := utility.ReadJSON(utility.NewRequestReader(r), log)
	if err != nil {
		as.LoggedError(w, r, http.StatusBadRequest, err)
		return
	}

	// enforce proper taskID and Execution
	log.Task = t.Id
	log.TaskExecution = t.Execution

	grip.Debug(message.Fields{
		"message":      "received test log",
		"task":         t.Id,
		"project":      t.Project,
		"requester":    t.Requester,
		"version":      t.Version,
		"display_name": t.DisplayName,
		"execution":    t.Execution,
		"log_length":   len(log.Lines),
	})

	if err := log.Insert(); err != nil {
		as.LoggedError(w, r, http.StatusInternalServerError, err)
		return
	}
	logReply := struct {
		Id string `json:"_id"`
	}{log.Id}
	gimlet.WriteJSON(w, logReply)
}

// AttachResults attaches the received results to the task in the database.
func (as *APIServer) AttachResults(w http.ResponseWriter, r *http.Request) {
	t := MustHaveTask(r)
	results := &task.LocalTestResults{}
	err := utility.ReadJSON(utility.NewRequestReader(r), results)
	if err != nil {
		as.LoggedError(w, r, http.StatusBadRequest, err)
		return
	}
	// set test result of task
	if err := t.SetResults(results.Results); err != nil {
		as.LoggedError(w, r, http.StatusInternalServerError, err)
		return
	}
	gimlet.WriteJSON(w, "test results successfully attached")
}

// FetchExpansionsForTask is an API hook for returning the
// project variables and parameters associated with a task.
func (as *APIServer) FetchExpansionsForTask(w http.ResponseWriter, r *http.Request) {
	t := MustHaveTask(r)
	projectVars, err := model.FindMergedProjectVars(t.Project)
	if err != nil {
		as.LoggedError(w, r, http.StatusInternalServerError, err)
		return
	}
	res := apimodels.ExpansionVars{
		Vars:        map[string]string{},
		PrivateVars: map[string]bool{},
	}
	if projectVars == nil {
		gimlet.WriteJSON(w, res)
		return
	}
	res.Vars = projectVars.GetVars(t)
	if projectVars.PrivateVars != nil {
		res.PrivateVars = projectVars.PrivateVars
	}
	v, err := model.VersionFindOne(model.VersionById(t.Version))
	if err != nil {
		as.LoggedError(w, r, http.StatusInternalServerError, err)
		return
	}
	if v == nil {
		as.LoggedError(w, r, http.StatusNotFound, errors.New("version not found"))
		return
	}
	projParams, err := model.FindParametersForVersion(v)
	if err != nil {
		as.LoggedError(w, r, http.StatusInternalServerError, err)
		return
	}
	for _, param := range projParams {
		// If the key doesn't exist the value will default to "" anyway; this prevents
		// an un-specified parameter from overwriting lower-priority expansions.
		if param.Value != "" {
			res.Vars[param.Key] = param.Value
		}
	}
	for _, param := range v.Parameters {
		// We will overwrite empty values here since these were explicitly user-specified.
		res.Vars[param.Key] = param.Value
	}

	gimlet.WriteJSON(w, res)
}

// AttachFiles updates file mappings for a task or build
func (as *APIServer) AttachFiles(w http.ResponseWriter, r *http.Request) {
	t := MustHaveTask(r)
	grip.Infoln("Attaching files to task:", t.Id)

	entry := &artifact.Entry{
		TaskId:          t.Id,
		TaskDisplayName: t.DisplayName,
		BuildId:         t.BuildId,
		Execution:       t.Execution,
		CreateTime:      time.Now(),
	}

	err := utility.ReadJSON(utility.NewRequestReader(r), &entry.Files)
	if err != nil {
		message := fmt.Sprintf("Error reading file definitions for task  %v: %v", t.Id, err)
		grip.Error(message)
		gimlet.WriteJSONError(w, message)
		return
	}

	if err := entry.Upsert(); err != nil {
		message := fmt.Sprintf("Error updating artifact file info for task %v: %v", t.Id, err)
		grip.Error(message)
		gimlet.WriteJSONInternalError(w, message)
		return
	}
	gimlet.WriteJSON(w, fmt.Sprintf("Artifact files for task %v successfully attached", t.Id))
}

// SetDownstreamParams updates file mappings for a task or build
func (as *APIServer) SetDownstreamParams(w http.ResponseWriter, r *http.Request) {
	t := MustHaveTask(r)
	grip.Infoln("Setting downstream expansions for task:", t.Id)

	var downstreamParams []patch.Parameter
	err := utility.ReadJSON(utility.NewRequestReader(r), &downstreamParams)
	if err != nil {
		errorMessage := fmt.Sprintf("Error reading downstream expansions for task %v: %v", t.Id, err)
		grip.Error(message.Fields{
			"message": errorMessage,
			"task_id": t.Id,
		})
		gimlet.WriteJSONError(w, errorMessage)
		return
	}
	p, err := patch.FindOne(patch.ByVersion(t.Version))

	if err != nil {
		errorMessage := fmt.Sprintf("error loading patch: %v: ", err)
		grip.Error(message.Fields{
			"message": errorMessage,
			"task_id": t.Id,
		})
		gimlet.WriteJSONError(w, errorMessage)
		return
	}

	if p == nil {
		errorMessage := "patch not found"
		grip.Error(message.Fields{
			"message": errorMessage,
			"task_id": t.Id,
		})
		gimlet.WriteJSONError(w, errorMessage)
		return
	}

	if err = p.SetDownstreamParameters(downstreamParams); err != nil {
		errorMessage := fmt.Sprintf("error setting patch parameters: %s", err)
		grip.Error(message.Fields{
			"message": errorMessage,
			"task_id": t.Id,
		})
		gimlet.WriteJSONInternalError(w, errorMessage)
		return
	}

	gimlet.WriteJSON(w, fmt.Sprintf("Downstream patches for %v have successfully been set", p.Id))
}

// NewPush updates when a task is pushing to s3 for s3 copy
func (as *APIServer) NewPush(w http.ResponseWriter, r *http.Request) {
	task := MustHaveTask(r)
	s3CopyReq := &apimodels.S3CopyRequest{}

	if err := utility.ReadJSON(utility.NewRequestReader(r), s3CopyReq); err != nil {
		as.LoggedError(w, r, http.StatusBadRequest, err)
		return
	}

	// Get the version for this task, so we can check if it has
	// any already-done pushes
	v, err := model.VersionFindOne(model.VersionById(task.Version))
	if err != nil {
		as.LoggedError(w, r, http.StatusInternalServerError,
			errors.Wrapf(err, "problem querying task %s with version id %s",
				task.Id, task.Version))
		return
	}

	// Check for an already-pushed file with this same file path,
	// but from a conflicting or newer commit sequence num
	if v == nil {
		as.LoggedError(w, r, http.StatusNotFound,
			errors.Errorf("no version found for '%s'", task.Id))
		return
	}

	copyToLocation := strings.Join([]string{s3CopyReq.S3DestinationBucket, s3CopyReq.S3DestinationPath}, "/")

	newestPushLog, err := model.FindPushLogAfter(copyToLocation, v.RevisionOrderNumber)
	if err != nil {
		as.LoggedError(w, r, http.StatusInternalServerError,
			errors.Wrapf(err, "problem querying for push log at '%s' for '%s'",
				copyToLocation, task.Id))
		return
	}
	if newestPushLog != nil {
		// the error is not being returned in order to avoid a retry
		grip.Warningln("conflict with existing pushed file:", copyToLocation)
		gimlet.WriteJSON(w, nil)
		return
	}

	// It's now safe to put the file in its permanent location.
	newPushLog := model.NewPushLog(v, task, copyToLocation)
	if err = newPushLog.Insert(); err != nil {
		as.LoggedError(w, r, http.StatusInternalServerError,
			errors.Wrapf(err, "failed to create new push log: %+v", newPushLog))
	}
	gimlet.WriteJSON(w, newPushLog)
}

// UpdatePushStatus updates the status for a file that a task is pushing to s3 for s3 copy
func (as *APIServer) UpdatePushStatus(w http.ResponseWriter, r *http.Request) {
	task := MustHaveTask(r)
	pushLog := &model.PushLog{}
	err := utility.ReadJSON(utility.NewRequestReader(r), pushLog)
	if err != nil {
		as.LoggedError(w, r, http.StatusBadRequest, err)
		return
	}

	err = errors.Wrapf(pushLog.UpdateStatus(pushLog.Status),
		"updating pushlog status failed for task %s", task.Id)
	if err != nil {
		grip.Error(message.WrapError(err, message.Fields{
			"task":      task.Id,
			"project":   task.Project,
			"version":   task.Version,
			"execution": task.Execution,
		}))
		as.LoggedError(w, r, http.StatusInternalServerError,
			errors.Wrapf(err, "updating pushlog status failed for task %s", task.Id))
		return
	}

	gimlet.WriteJSON(w, nil)
}

// AppendTaskLog appends the received logs to the task's internal logs.
func (as *APIServer) AppendTaskLog(w http.ResponseWriter, r *http.Request) {
	if as.GetSettings().ServiceFlags.TaskLoggingDisabled {
		http.Error(w, "task logging is disabled", http.StatusConflict)
		return
	}
	t := MustHaveTask(r)
	taskLog := &model.TaskLog{}
	if err := gimlet.GetJSON(r.Body, taskLog); err != nil {
		as.LoggedError(w, r, http.StatusBadRequest, errors.Wrap(err, "unable to read logs from request"))
		return
	}

	taskLog.TaskId = t.Id
	taskLog.Execution = t.Execution

	if err := taskLog.Insert(); err != nil {
		as.LoggedError(w, r, http.StatusInternalServerError, err)
		return
	}

	gimlet.WriteJSON(w, "Logs added")
}

// FetchTask loads the task from the database and sends it to the requester.
func (as *APIServer) FetchTask(w http.ResponseWriter, r *http.Request) {
	t := MustHaveTask(r)
	gimlet.WriteJSON(w, t)
}

// Heartbeat handles heartbeat pings from Evergreen agents. If the heartbeating
// task is marked to be aborted, the abort response is sent.
func (as *APIServer) Heartbeat(w http.ResponseWriter, r *http.Request) {
	t := MustHaveTask(r)

	heartbeatResponse := apimodels.HeartbeatResponse{}
	if t.Aborted {
		grip.Noticef("Sending abort signal for task %s", t.Id)
		heartbeatResponse.Abort = true
	}

	if err := t.UpdateHeartbeat(); err != nil {
		grip.Warningf("Error updating heartbeat for task %s: %+v", t.Id, err)
	}
	gimlet.WriteJSON(w, heartbeatResponse)
}

// fetchProjectRef returns a project ref given the project identifier
func (as *APIServer) fetchProjectRef(w http.ResponseWriter, r *http.Request) {
	id := gimlet.GetVars(r)["identifier"]
	projectRef, err := model.FindMergedProjectRef(id, "", true)
	if err != nil {
		as.LoggedError(w, r, http.StatusInternalServerError, err)
		return
	}
	if projectRef == nil {
		http.Error(w, fmt.Sprintf("no project found named '%v'", id), http.StatusNotFound)
		return
	}
	gimlet.WriteJSON(w, projectRef)
}

// listProjects returns the projects merged with the repo settings
func (as *APIServer) listProjects(w http.ResponseWriter, r *http.Request) {
	allProjs, err := model.FindAllMergedTrackedProjectRefs()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	gimlet.WriteJSON(w, allProjs)
}

func (as *APIServer) listTasks(w http.ResponseWriter, r *http.Request) {
	project := MustHaveProject(r)

	// zero out the depends on and commands fields because they are
	// unnecessary and may not get marshaled properly
	for i := range project.Tasks {
		project.Tasks[i].DependsOn = []model.TaskUnitDependency{}
		project.Tasks[i].Commands = []model.PluginCommandConf{}

	}
	gimlet.WriteJSON(w, project.Tasks)
}
func (as *APIServer) listVariants(w http.ResponseWriter, r *http.Request) {
	project := MustHaveProject(r)

	gimlet.WriteJSON(w, project.BuildVariants)
}

// validateProjectConfig returns a slice containing a list of any errors
// found in validating the given project configuration
func (as *APIServer) validateProjectConfig(w http.ResponseWriter, r *http.Request) {
	body := utility.NewRequestReader(r)
	defer body.Close()

	bytes, err := ioutil.ReadAll(body)
	if err != nil {
		gimlet.WriteJSONError(w, fmt.Sprintf("Error reading request body: %v", err))
		return
	}

	input := validator.ValidationInput{}
	if err := json.Unmarshal(bytes, &input); err != nil {
		// try the legacy structure
		input.ProjectYaml = bytes
		input.IncludeLong = true // this is legacy behavior
	}

	project := &model.Project{}
	var projectConfig *model.ProjectConfig
	ctx := context.Background()
	opts := &model.GetProjectOpts{
		ReadFileFrom: model.ReadFromLocal,
	}
	validationErr := validator.ValidationError{}
	if _, err = model.LoadProjectInto(ctx, input.ProjectYaml, opts, "", project); err != nil {
		validationErr.Message = err.Error()
		gimlet.WriteJSONError(w, validator.ValidationErrors{validationErr})
		return
	}
	if projectConfig, err = model.CreateProjectConfig(input.ProjectYaml, ""); err != nil {
		validationErr.Message = err.Error()
		gimlet.WriteJSONError(w, validator.ValidationErrors{validationErr})
		return
	}

	errs := validator.ValidationErrors{}
	if input.ProjectID != "" {
		projectRef, err := model.FindMergedProjectRef(input.ProjectID, "", false)
		if err != nil {
			validationErr = validator.ValidationError{
				Message: "error finding project; validation will proceed without checking project settings",
				Level:   validator.Warning,
			}
			errs = append(errs, validationErr)
		} else if projectRef == nil {
			validationErr = validator.ValidationError{
				Message: "project does not exist; validation will proceed without checking project settings",
				Level:   validator.Warning,
			}
			errs = append(errs, validationErr)
		} else {
			isConfigDefined := projectConfig != nil
			errs = append(errs, validator.CheckProjectSettings(project, projectRef, isConfigDefined)...)
		}
	} else {
		validationErr = validator.ValidationError{
			Message: "no project specified; validation will proceed without checking project settings",
			Level:   validator.Warning,
		}
		errs = append(errs, validationErr)
	}

	errs = append(errs, validator.CheckProjectErrors(project, input.IncludeLong)...)
	if projectConfig != nil {
		errs = append(errs, validator.CheckProjectConfigErrors(projectConfig)...)
	}

	if input.Quiet {
		errs = errs.AtLevel(validator.Error)
	} else {
		errs = append(errs, validator.CheckProjectWarnings(project)...)
	}

	if len(errs) > 0 {
		gimlet.WriteJSONError(w, errs)
		return
	}
	gimlet.WriteJSON(w, validator.ValidationErrors{})
}

// LoggedError logs the given error and writes an HTTP response with its details formatted
// as JSON if the request headers indicate that it's acceptable (or plaintext otherwise).
func (as *APIServer) LoggedError(w http.ResponseWriter, r *http.Request, code int, err error) {
	if err == nil {
		return
	}

	grip.Error(message.WrapError(err, message.Fields{
		"method":     r.Method,
		"url":        r.URL.String(),
		"code":       code,
		"len":        r.ContentLength,
		"spawn_host": r.Host,
		"request":    gimlet.GetRequestID(r.Context()),
	}))

	var resp gimlet.Responder

	// if JSON is the preferred content type for the request, reply with a json message
	if strings.HasPrefix(r.Header.Get("accept"), "application/json") {
		resp = gimlet.MakeJSONErrorResponder(err)
	} else {
		resp = gimlet.MakeTextErrorResponder(err)
	}

	if err := resp.SetStatus(code); err != nil {
		grip.Warning(errors.WithStack(resp.SetStatus(http.StatusInternalServerError)))
	}

	gimlet.WriteResponse(w, resp)
}

func (as *APIServer) Cedar(w http.ResponseWriter, r *http.Request) {
	gimlet.WriteJSON(w, &apimodels.CedarConfig{
		BaseURL:  as.Settings.Cedar.BaseURL,
		RPCPort:  as.Settings.Cedar.RPCPort,
		Username: as.Settings.Cedar.User,
		APIKey:   as.Settings.Cedar.APIKey,
	})
}

// GetSettings returns the global evergreen settings.
func (as *APIServer) GetSettings() evergreen.Settings {
	return as.Settings
}

// NewRouter returns the root router for all APIServer endpoints.
func (as *APIServer) GetServiceApp() *gimlet.APIApp {
	requireProject := gimlet.WrapperMiddleware(as.requireProject)
	requireTaskSecret := gimlet.WrapperMiddleware(as.requireTaskStrict)
	requireUser := gimlet.NewRequireAuthHandler()
	requireTask := gimlet.WrapperMiddleware(as.requireTask)
	requireHost := gimlet.WrapperMiddleware(as.requireHost)
	viewTasks := route.RequiresProjectPermission(evergreen.PermissionTasks, evergreen.TasksView)
	submitPatch := route.RequiresProjectPermission(evergreen.PermissionPatches, evergreen.PatchSubmit)

	app := gimlet.NewApp()
	app.SetPrefix("/api")
	app.NoVersions = true
	app.SimpleVersions = true

	// Project lookup and validation routes
	app.AddRoute("/ref/{identifier}").Handler(as.fetchProjectRef).Get()
	app.AddRoute("/validate").Handler(as.validateProjectConfig).Post()

	// Internal status reporting
	app.AddRoute("/status/consistent_task_assignment").Handler(as.consistentTaskAssignment).Get()
	app.AddRoute("/status/stuck_hosts").Handler(as.getStuckHosts).Get()
	app.AddRoute("/status/info").Handler(as.serviceStatusSimple).Get()
	app.AddRoute("/task_queue").Handler(as.getTaskQueueSizes).Get()
	app.AddRoute("/task_queue/limit").Handler(as.checkTaskQueueSize).Get()

	// CLI Operation Backends
	app.AddRoute("/tasks/{projectId}").Wrap(requireUser, requireProject, viewTasks).Handler(as.listTasks).Get()
	app.AddRoute("/variants/{projectId}").Wrap(requireUser, requireProject, viewTasks).Handler(as.listVariants).Get()
	app.AddRoute("/projects").Wrap(requireUser).Handler(as.listProjects).Get()

	// Patches
	app.PrefixRoute("/patches").Route("/").Wrap(requireUser).Handler(as.submitPatch).Put()
	app.PrefixRoute("/patches").Route("/mine").Wrap(requireUser).Handler(as.listPatches).Get()
	app.PrefixRoute("/patches").Route("/{patchId:\\w+}").Wrap(requireUser, viewTasks).Handler(as.summarizePatch).Get()
	app.PrefixRoute("/patches").Route("/{patchId:\\w+}").Wrap(requireUser, submitPatch).Handler(as.existingPatchRequest).Post()
	app.PrefixRoute("/patches").Route("/{patchId:\\w+}/{projectId}/modules").Wrap(requireUser, requireProject, viewTasks).Handler(as.listPatchModules).Get()
	app.PrefixRoute("/patches").Route("/{patchId:\\w+}/modules").Wrap(requireUser, submitPatch).Handler(as.deletePatchModule).Delete()
	app.PrefixRoute("/patches").Route("/{patchId:\\w+}/modules").Wrap(requireUser, submitPatch).Handler(as.updatePatchModule).Post()

	// SpawnHosts
	app.Route().Prefix("/spawn").Wrap(requireUser).Route("/{instance_id:[\\w_\\-\\@]+}/").Handler(as.hostInfo).Get()
	app.Route().Prefix("/spawn").Wrap(requireUser).Route("/{instance_id:[\\w_\\-\\@]+}/").Handler(as.modifyHost).Post()
	app.Route().Prefix("/spawns").Wrap(requireUser).Route("/").Handler(as.requestHost).Put()
	app.Route().Prefix("/spawns").Wrap(requireUser).Route("/{user}/").Handler(as.hostsInfoForUser).Get()
	app.Route().Prefix("/spawns").Wrap(requireUser).Route("/distros/list/").Handler(as.listDistros).Get()
	app.AddRoute("/dockerfile").Handler(getDockerfile).Get()

	// Agent routes
	// NOTE: new agent routes should be written in REST v2. The ones here are
	// legacy routes.
	app.Route().Version(2).Route("/agent/setup").Wrap(requireHost).Handler(as.agentSetup).Get()
	app.Route().Version(2).Route("/agent/next_task").Wrap(requireHost).Handler(as.NextTask).Get()
	app.Route().Version(2).Route("/agent/cedar_config").Wrap(requireHost).Handler(as.Cedar).Get()
	app.Route().Version(2).Route("/task/{taskId}/end").Wrap(requireTaskSecret, requireHost).Handler(as.EndTask).Post()
	app.Route().Version(2).Route("/task/{taskId}/start").Wrap(requireTaskSecret, requireHost).Handler(as.StartTask).Post()
	app.Route().Version(2).Route("/task/{taskId}/log").Wrap(requireTaskSecret, requireHost).Handler(as.AppendTaskLog).Post()
	app.Route().Version(2).Route("/task/{taskId}/").Wrap(requireTaskSecret).Handler(as.FetchTask).Get()
	app.Route().Version(2).Route("/task/{taskId}/fetch_vars").Wrap(requireTaskSecret).Handler(as.FetchExpansionsForTask).Get()
	app.Route().Version(2).Route("/task/{taskId}/heartbeat").Wrap(requireTaskSecret, requireHost).Handler(as.Heartbeat).Post()
	app.Route().Version(2).Route("/task/{taskId}/results").Wrap(requireTaskSecret, requireHost).Handler(as.AttachResults).Post()
	app.Route().Version(2).Route("/task/{taskId}/test_logs").Wrap(requireTaskSecret, requireHost).Handler(as.AttachTestLog).Post()
	app.Route().Version(2).Route("/task/{taskId}/files").Wrap(requireTask, requireHost).Handler(as.AttachFiles).Post()
	app.Route().Version(2).Route("/task/{taskId}/distro_view").Wrap(requireTask, requireHost).Handler(as.GetDistroView).Get()
	app.Route().Version(2).Route("/task/{taskId}/parser_project").Wrap(requireTaskSecret).Handler(as.GetParserProject).Get()
	app.Route().Version(2).Route("/task/{taskId}/project_ref").Wrap(requireTaskSecret).Handler(as.GetProjectRef).Get()
	app.Route().Version(2).Route("/task/{taskId}/expansions").Wrap(requireTask, requireHost).Handler(as.GetExpansions).Get()
	app.Route().Version(2).Route("/task/{taskId}/new_push").Wrap(requireTaskSecret).Handler(as.NewPush).Post()
	app.Route().Version(2).Route("/task/{taskId}/update_push_status").Wrap(requireTaskSecret).Handler(as.UpdatePushStatus).Post()

	// plugins
	app.Route().Version(2).Prefix("/task/{taskId}").Route("/git/patchfile/{patchfile_id}").Wrap(requireTaskSecret).Handler(as.gitServePatchFile).Get()
	app.Route().Version(2).Prefix("/task/{taskId}").Route("/git/patch").Wrap(requireTaskSecret).Handler(as.gitServePatch).Get()
	app.Route().Version(2).Prefix("/task/{taskId}").Route("/keyval/inc").Wrap(requireTask).Handler(as.keyValPluginInc).Post()
	app.Route().Version(2).Prefix("/task/{taskId}").Route("/manifest/load").Wrap(requireTask).Handler(as.manifestLoadHandler).Get()
	app.Route().Version(2).Prefix("/task/{taskId}").Route("/downstreamParams").Wrap(requireTask).Handler(as.SetDownstreamParams).Post()
	app.Route().Version(2).Prefix("/task/{taskId}").Route("/json/tags/{task_name}/{name}").Wrap(requireTask).Handler(as.getTaskJSONTagsForTask).Get()
	app.Route().Version(2).Prefix("/task/{taskId}").Route("/json/history/{task_name}/{name}").Wrap(requireTask).Handler(as.getTaskJSONTaskHistory).Get()
	app.Route().Version(2).Prefix("/task/{taskId}").Route("/json/data/{name}").Wrap(requireTask).Handler(as.insertTaskJSON).Post()
	app.Route().Version(2).Prefix("/task/{taskId}").Route("/json/data/{task_name}/{name}").Wrap(requireTask).Handler(as.getTaskJSONByName).Get()
	app.Route().Version(2).Prefix("/task/{taskId}").Route("/json/data/{task_name}/{name}/{variant}").Wrap(requireTask).Handler(as.getTaskJSONForVariant).Get()

	return app
}
