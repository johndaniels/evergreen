package route

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/evergreen-ci/evergreen"
	"github.com/evergreen-ci/evergreen/model/user"
	"github.com/evergreen-ci/evergreen/rest/data"
	"github.com/evergreen-ci/evergreen/rest/model"
	"github.com/evergreen-ci/gimlet"
	"github.com/evergreen-ci/gimlet/rolemanager"
	"github.com/evergreen-ci/utility"
	"github.com/mongodb/grip"
	"github.com/mongodb/grip/message"
	"github.com/pkg/errors"
)

////////////////////////////////////////////////////////////////////////
//
// POST /rest/v2/user/settings

type userSettingsPostHandler struct {
	settings model.APIUserSettings
}

func makeSetUserConfig() gimlet.RouteHandler {
	return &userSettingsPostHandler{}
}

func (h *userSettingsPostHandler) Factory() gimlet.RouteHandler {
	return &userSettingsPostHandler{}
}

func (h *userSettingsPostHandler) Parse(ctx context.Context, r *http.Request) error {
	h.settings = model.APIUserSettings{}
	return errors.Wrap(utility.ReadJSON(r.Body, &h.settings), "reading user settings from JSON request body")
}

func (h *userSettingsPostHandler) Run(ctx context.Context) gimlet.Responder {
	u := MustHaveUser(ctx)
	userSettings, err := model.UpdateUserSettings(ctx, u, h.settings)
	if err != nil {
		return gimlet.MakeJSONInternalErrorResponder(errors.Wrapf(err, "updating user settings for user '%s'", u.Username()))
	}

	if err = data.UpdateSettings(u, *userSettings); err != nil {
		return gimlet.MakeJSONInternalErrorResponder(errors.Wrapf(err, "saving updated settings for user '%s'", u.Username()))
	}

	if h.settings.SpruceFeedback != nil {
		h.settings.SpruceFeedback.SubmittedAt = model.ToTimePtr(time.Now())
		h.settings.SpruceFeedback.User = utility.ToStringPtr(u.Username())
		if err = data.SubmitFeedback(*h.settings.SpruceFeedback); err != nil {
			return gimlet.MakeJSONInternalErrorResponder(errors.Wrap(err, "submitting Spruce feedback"))
		}
	}

	return gimlet.NewJSONResponse(struct{}{})
}

////////////////////////////////////////////////////////////////////////
//
// GET /rest/v2/user/settings

type userSettingsGetHandler struct{}

func makeFetchUserConfig() gimlet.RouteHandler {
	return &userSettingsGetHandler{}
}

func (h *userSettingsGetHandler) Factory() gimlet.RouteHandler                     { return h }
func (h *userSettingsGetHandler) Parse(ctx context.Context, r *http.Request) error { return nil }

func (h *userSettingsGetHandler) Run(ctx context.Context) gimlet.Responder {
	u := MustHaveUser(ctx)

	apiSettings := model.APIUserSettings{}
	if err := apiSettings.BuildFromService(u.Settings); err != nil {
		return gimlet.MakeJSONInternalErrorResponder(errors.Wrapf(err, "converting settings for user '%s' to API model", u.Username()))
	}

	return gimlet.NewJSONResponse(apiSettings)
}

type userPermissionsPostHandler struct {
	rm          gimlet.RoleManager
	userID      string
	permissions RequestedPermissions
}

type RequestedPermissions struct {
	ResourceType string             `json:"resource_type"`
	Resources    []string           `json:"resources"`
	Permissions  gimlet.Permissions `json:"permissions"`
}

func makeModifyUserPermissions(rm gimlet.RoleManager) gimlet.RouteHandler {
	return &userPermissionsPostHandler{
		rm: rm,
	}
}

func (h *userPermissionsPostHandler) Factory() gimlet.RouteHandler {
	return &userPermissionsPostHandler{
		rm: h.rm,
	}
}

func (h *userPermissionsPostHandler) Parse(ctx context.Context, r *http.Request) error {
	vars := gimlet.GetVars(r)
	h.userID = vars["user_id"]
	if h.userID == "" {
		return errors.New("no user found")
	}
	permissions := RequestedPermissions{}
	if err := utility.ReadJSON(r.Body, &permissions); err != nil {
		return errors.Wrap(err, "reading permissions from JSON request body")
	}
	if !utility.StringSliceContains(evergreen.ValidResourceTypes, permissions.ResourceType) {
		return errors.Errorf("invalid resource type '%s'", permissions.ResourceType)
	}
	if len(permissions.Resources) == 0 {
		return errors.New("resources cannot be empty")
	}
	h.permissions = permissions

	return nil
}

func (h *userPermissionsPostHandler) Run(ctx context.Context) gimlet.Responder {
	u, err := user.FindOneById(h.userID)
	if err != nil {
		return gimlet.MakeJSONInternalErrorResponder(errors.Wrapf(err, "getting user '%s'", h.userID))
	}
	if u == nil {
		return gimlet.MakeJSONErrorResponder(gimlet.ErrorResponse{
			Message:    fmt.Sprintf("user '%s' not found", h.userID),
			StatusCode: http.StatusNotFound,
		})
	}

	newRole, err := rolemanager.MakeRoleWithPermissions(h.rm, h.permissions.ResourceType, h.permissions.Resources, h.permissions.Permissions)
	if err != nil {
		return gimlet.NewTextInternalErrorResponse(err.Error())
	}
	if err = u.AddRole(newRole.ID); err != nil {
		return gimlet.NewTextInternalErrorResponse(err.Error())
	}

	return gimlet.NewJSONResponse(struct{}{})
}

type deletePermissionsRequest struct {
	ResourceType string `json:"resource_type"`
	ResourceId   string `json:"resource_id"`
}

const allResourceType = "all"

type userPermissionsDeleteHandler struct {
	rm           gimlet.RoleManager
	userID       string
	resourceType string
	resourceId   string
}

func makeDeleteUserPermissions(rm gimlet.RoleManager) gimlet.RouteHandler {
	return &userPermissionsDeleteHandler{
		rm: rm,
	}
}

func (h *userPermissionsDeleteHandler) Factory() gimlet.RouteHandler {
	return &userPermissionsDeleteHandler{
		rm: h.rm,
	}
}

func (h *userPermissionsDeleteHandler) Parse(ctx context.Context, r *http.Request) error {
	vars := gimlet.GetVars(r)
	h.userID = vars["user_id"]
	if h.userID == "" {
		return errors.New("no user found")
	}
	request := deletePermissionsRequest{}
	if err := utility.ReadJSON(r.Body, &request); err != nil {
		return errors.Wrap(err, "reading delete request from JSON request body")
	}
	h.resourceType = request.ResourceType
	h.resourceId = request.ResourceId
	if !utility.StringSliceContains(evergreen.ValidResourceTypes, h.resourceType) && h.resourceType != allResourceType {
		return errors.Errorf("invalid resource type '%s'", h.resourceType)
	}
	if h.resourceType != allResourceType && h.resourceId == "" {
		return errors.New("must specify a resource ID to delete permissions for unless deleting all permissions")
	}

	return nil
}

func (h *userPermissionsDeleteHandler) Run(ctx context.Context) gimlet.Responder {
	u, err := user.FindOneById(h.userID)
	if err != nil {
		return gimlet.MakeJSONInternalErrorResponder(errors.Wrapf(err, "finding user '%s'", h.userID))
	}
	if u == nil {
		return gimlet.MakeJSONErrorResponder(gimlet.ErrorResponse{
			Message:    fmt.Sprintf("user '%s' not found", h.userID),
			StatusCode: http.StatusNotFound,
		})
	}

	if h.resourceType == allResourceType {
		err = u.DeleteAllRoles()
		if err != nil {
			return gimlet.MakeJSONInternalErrorResponder(errors.Wrapf(err, "deleting all roles for user '%s'", u.Username()))
		}
		return gimlet.NewJSONResponse(struct{}{})
	}

	roles, err := h.rm.GetRoles(u.Roles())
	if err != nil {
		return gimlet.MakeJSONInternalErrorResponder(errors.Wrapf(err, "getting current roles for user '%s'", u.Username()))
	}
	rolesToCheck := []gimlet.Role{}
	// don't remove basic access, just special access
	for _, r := range roles {
		if !utility.StringSliceContains(evergreen.BasicAccessRoles, r.ID) {
			rolesToCheck = append(rolesToCheck, r)
		}
	}
	if len(rolesToCheck) == 0 {
		gimlet.NewJSONResponse(struct{}{})
	}

	rolesForResource, err := h.rm.FilterForResource(rolesToCheck, h.resourceId, h.resourceType)
	if err != nil {
		return gimlet.MakeJSONInternalErrorResponder(errors.Wrapf(err, "filtering user roles for resource '%s'", h.resourceId))
	}
	rolesToRemove := []string{}
	for _, r := range rolesForResource {
		rolesToRemove = append(rolesToRemove, r.ID)
	}

	grip.Info(message.Fields{
		"removed_roles": rolesToRemove,
		"user":          u.Id,
		"resource_type": h.resourceType,
		"resource_id":   h.resourceId,
	})
	err = u.DeleteRoles(rolesToRemove)
	if err != nil {
		return gimlet.MakeJSONInternalErrorResponder(errors.Wrapf(err, "deleting roles for user '%s'", u.Username()))
	}
	return gimlet.NewJSONResponse(struct{}{})
}

////////////////////////////////////////////////////////////////////////
//
// GET /users/permissions

type UsersPermissionsInput struct {
	ResourceId   string `json:"resource_id"`
	ResourceType string `json:"resource_type"`
}

// UserPermissionsResult is a map from userId to their highest permission for the resource
type UsersPermissionsResult map[string]gimlet.Permissions

type allUsersPermissionsGetHandler struct {
	rm    gimlet.RoleManager
	input UsersPermissionsInput
}

func makeGetAllUsersPermissions(rm gimlet.RoleManager) gimlet.RouteHandler {
	return &allUsersPermissionsGetHandler{
		rm: rm,
	}
}

func (h *allUsersPermissionsGetHandler) Factory() gimlet.RouteHandler {
	return &allUsersPermissionsGetHandler{
		rm: h.rm,
	}
}

func (h *allUsersPermissionsGetHandler) Parse(ctx context.Context, r *http.Request) error {
	err := utility.ReadJSON(r.Body, &h.input)
	if err != nil {
		return errors.Wrap(err, "reading permissions request from JSON request body")
	}
	if !utility.StringSliceContains(evergreen.ValidResourceTypes, h.input.ResourceType) {
		return errors.Errorf("invalid resource type '%s'", h.input.ResourceType)
	}
	if h.input.ResourceId == "" {
		return errors.New("resource ID is required")
	}
	return nil
}

func (h *allUsersPermissionsGetHandler) Run(ctx context.Context) gimlet.Responder {
	// get roles for resource ID
	allRoles, err := h.rm.GetAllRoles()
	if err != nil {
		return gimlet.NewJSONInternalErrorResponse(errors.Wrap(err, "getting all roles"))
	}

	roles, err := h.rm.FilterForResource(allRoles, h.input.ResourceId, h.input.ResourceType)
	if err != nil {
		return gimlet.NewJSONInternalErrorResponse(errors.Wrapf(err, "finding roles for resource '%s'", h.input.ResourceId))
	}
	roleIds := []string{}
	permissionsMap := map[string]gimlet.Permissions{}
	for _, role := range roles {
		// don't include basic roles
		if !utility.StringSliceContains(evergreen.BasicAccessRoles, role.ID) {
			roleIds = append(roleIds, role.ID)
			permissionsMap[role.ID] = role.Permissions
		}
	}
	// get users with roles
	usersWithRoles, err := user.FindHumanUsersByRoles(roleIds)
	if err != nil {
		return gimlet.NewJSONInternalErrorResponse(errors.Wrapf(err, "finding users for roles %v", roleIds))
	}
	// map from users to their highest permissions
	res := UsersPermissionsResult{}
	for _, u := range usersWithRoles {
		for _, userRole := range u.SystemRoles {
			permissions, ok := permissionsMap[userRole]
			if ok {
				res[u.Username()] = getMaxPermissions(res[u.Username()], permissions)
			}
		}
	}

	return gimlet.NewJSONResponse(res)
}

func getMaxPermissions(p1, p2 gimlet.Permissions) gimlet.Permissions {
	res := gimlet.Permissions{}
	if p1 != nil {
		res = p1
	}
	for key, val := range p2 {
		if res[key] < val {
			res[key] = val
		}
	}
	return res
}

////////////////////////////////////////////////////////////////////////
//
// GET /users/{user_id}/permissions

type userPermissionsGetHandler struct {
	rm     gimlet.RoleManager
	userID string
}

func makeGetUserPermissions(rm gimlet.RoleManager) gimlet.RouteHandler {
	return &userPermissionsGetHandler{
		rm: rm,
	}
}

func (h *userPermissionsGetHandler) Factory() gimlet.RouteHandler {
	return &userPermissionsGetHandler{
		rm: h.rm,
	}
}

func (h *userPermissionsGetHandler) Parse(ctx context.Context, r *http.Request) error {
	vars := gimlet.GetVars(r)
	h.userID = vars["user_id"]
	if h.userID == "" {
		return errors.New("no user found")
	}
	return nil
}

func (h *userPermissionsGetHandler) Run(ctx context.Context) gimlet.Responder {
	u, err := user.FindOneById(h.userID)
	if err != nil {
		grip.Error(message.WrapError(err, message.Fields{
			"message": "error finding user",
			"route":   "userPermissionsGetHandler",
		}))
		return gimlet.NewJSONInternalErrorResponse(errors.Wrapf(err, "finding user '%s'", h.userID))
	}
	if u == nil {
		return gimlet.NewJSONInternalErrorResponse(errors.Errorf("user '%s' not found", h.userID))
	}
	rolesToSearch, _ := utility.StringSliceSymmetricDifference(u.SystemRoles, evergreen.BasicAccessRoles)
	// filter out the roles that everybody has automatically
	permissions, err := rolemanager.PermissionSummaryForRoles(ctx, rolesToSearch, h.rm)
	if err != nil {
		return gimlet.NewJSONInternalErrorResponse(errors.Wrapf(err, "getting permissions for user '%s'", h.userID))
	}
	return gimlet.NewJSONResponse(permissions)
}

type rolesPostRequest struct {
	Roles      []string `json:"roles"`
	CreateUser bool     `json:"create_user"`
}

type userRolesPostHandler struct {
	rm         gimlet.RoleManager
	userID     string
	roles      []string
	createUser bool
}

func makeModifyUserRoles(rm gimlet.RoleManager) gimlet.RouteHandler {
	return &userRolesPostHandler{
		rm: rm,
	}
}

func (h *userRolesPostHandler) Factory() gimlet.RouteHandler {
	return &userRolesPostHandler{
		rm: h.rm,
	}
}

func (h *userRolesPostHandler) Parse(ctx context.Context, r *http.Request) error {
	var request rolesPostRequest
	if err := utility.ReadJSON(r.Body, &request); err != nil {
		return errors.Wrap(err, "reading role modification request from JSON request body")
	}
	if len(request.Roles) == 0 {
		return errors.New("must specify at least 1 role to add")
	}
	h.roles = request.Roles
	h.createUser = request.CreateUser
	vars := gimlet.GetVars(r)
	h.userID = vars["user_id"]

	return nil
}

func (h *userRolesPostHandler) Run(ctx context.Context) gimlet.Responder {
	u, err := user.FindOneById(h.userID)
	if err != nil {
		return gimlet.MakeJSONInternalErrorResponder(errors.Wrapf(err, "finding user '%s'", h.userID))
	}
	if u == nil {
		if h.createUser {
			um := evergreen.GetEnvironment().UserManager()
			newUser := user.DBUser{
				Id:          h.userID,
				SystemRoles: h.roles,
			}
			_, err = um.GetOrCreateUser(&newUser)
			if err != nil {
				return gimlet.MakeJSONInternalErrorResponder(errors.Wrapf(err, "creating new user '%s'", h.userID))
			}
			return gimlet.NewJSONResponse(struct{}{})
		} else {
			return gimlet.MakeJSONErrorResponder(gimlet.ErrorResponse{
				Message:    fmt.Sprintf("user '%s' not found", h.userID),
				StatusCode: http.StatusNotFound,
			})
		}
	}
	dbRoles, err := h.rm.GetRoles(h.roles)
	if err != nil {
		return gimlet.MakeJSONErrorResponder(gimlet.ErrorResponse{
			Message:    errors.Wrapf(err, "finding roles for user '%s'", u.Username()).Error(),
			StatusCode: http.StatusNotFound,
		})
	}
	foundRoles := []string{}
	for _, found := range dbRoles {
		foundRoles = append(foundRoles, found.ID)
	}
	nonexistent, _ := utility.StringSliceSymmetricDifference(h.roles, foundRoles)
	if len(nonexistent) > 0 {
		return gimlet.MakeJSONErrorResponder(gimlet.ErrorResponse{
			Message:    fmt.Sprintf("roles not found: %v", nonexistent),
			StatusCode: http.StatusNotFound,
		})
	}
	for _, toAdd := range h.roles {
		if err = u.AddRole(toAdd); err != nil {
			return gimlet.MakeJSONInternalErrorResponder(errors.Wrapf(err, "adding role '%s' to user '%s'", toAdd, u.Username()))
		}
	}

	return gimlet.NewJSONResponse(struct{}{})
}

type UsersWithRoleResponse struct {
	Users []*string `json:"users"`
}

type usersWithRoleGetHandler struct {
	role string
}

func makeGetUsersWithRole() gimlet.RouteHandler {
	return &usersWithRoleGetHandler{}
}

func (h *usersWithRoleGetHandler) Factory() gimlet.RouteHandler {
	return &usersWithRoleGetHandler{}
}

func (h *usersWithRoleGetHandler) Parse(ctx context.Context, r *http.Request) error {
	vars := gimlet.GetVars(r)
	h.role = vars["role_id"]
	return nil
}

func (h *usersWithRoleGetHandler) Run(ctx context.Context) gimlet.Responder {
	users, err := user.FindByRole(h.role)
	if err != nil {
		return gimlet.MakeJSONInternalErrorResponder(err)
	}
	res := []*string{}
	for idx := range users {
		res = append(res, &users[idx].Id)
	}
	return gimlet.NewJSONResponse(&UsersWithRoleResponse{Users: res})
}

type serviceUserPostHandler struct {
	u *model.APIDBUser
}

func makeUpdateServiceUser() gimlet.RouteHandler {
	return &serviceUserPostHandler{
		u: &model.APIDBUser{},
	}
}

func (h *serviceUserPostHandler) Factory() gimlet.RouteHandler {
	return &serviceUserPostHandler{
		u: &model.APIDBUser{},
	}
}

func (h *serviceUserPostHandler) Parse(ctx context.Context, r *http.Request) error {
	h.u = &model.APIDBUser{}
	if err := utility.ReadJSON(r.Body, h.u); err != nil {
		return errors.Wrap(err, "reading user from JSON request body")
	}
	if h.u.UserID == nil || *h.u.UserID == "" {
		return errors.New("must specify user ID")
	}
	return nil
}

func (h *serviceUserPostHandler) Run(ctx context.Context) gimlet.Responder {
	if h.u == nil {
		return gimlet.NewJSONErrorResponse("no user read from request body")
	}
	err := data.AddOrUpdateServiceUser(*h.u)
	if err != nil {
		return gimlet.MakeJSONInternalErrorResponder(errors.Wrapf(err, "adding/updating service user '%s'", utility.FromStringPtr(h.u.UserID)))
	}
	return gimlet.NewJSONResponse(struct{}{})
}

type serviceUserDeleteHandler struct {
	username string
}

func makeDeleteServiceUser() gimlet.RouteHandler {
	return &serviceUserDeleteHandler{}
}

func (h *serviceUserDeleteHandler) Factory() gimlet.RouteHandler {
	return &serviceUserDeleteHandler{}
}

func (h *serviceUserDeleteHandler) Parse(ctx context.Context, r *http.Request) error {
	h.username = r.FormValue("id")
	if h.username == "" {
		return errors.New("user ID must be specified")
	}

	return nil
}

func (h *serviceUserDeleteHandler) Run(ctx context.Context) gimlet.Responder {
	err := user.DeleteServiceUser(h.username)
	if err != nil {
		return gimlet.MakeJSONInternalErrorResponder(errors.Wrapf(err, "deleting service user '%s'", h.username))
	}

	return gimlet.NewJSONResponse(struct{}{})
}

type serviceUsersGetHandler struct {
}

func makeGetServiceUsers() gimlet.RouteHandler {
	return &serviceUsersGetHandler{}
}

func (h *serviceUsersGetHandler) Factory() gimlet.RouteHandler {
	return &serviceUsersGetHandler{}
}

func (h *serviceUsersGetHandler) Parse(ctx context.Context, r *http.Request) error {
	return nil
}

func (h *serviceUsersGetHandler) Run(ctx context.Context) gimlet.Responder {
	users, err := data.GetServiceUsers()
	if err != nil {
		return gimlet.MakeJSONInternalErrorResponder(errors.Wrap(err, "getting all service users"))
	}

	return gimlet.NewJSONResponse(users)
}
