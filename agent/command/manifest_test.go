package command

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/evergreen-ci/evergreen/agent/internal/client"
	agentutil "github.com/evergreen-ci/evergreen/agent/internal/testutil"
	"github.com/evergreen-ci/evergreen/db"
	"github.com/evergreen-ci/evergreen/model/manifest"
	modelutil "github.com/evergreen-ci/evergreen/model/testutil"
	"github.com/evergreen-ci/evergreen/testutil"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
)

// ManifestFetchCmd integration tests

func TestManifestLoad(t *testing.T) {
	require.NoError(t, db.ClearCollections(manifest.Collection),
		"error clearing test collections")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	comm := client.NewMock("http://localhost.com")
	env := testutil.NewEnvironment(ctx, t)
	testConfig := env.Settings()

	testutil.ConfigureIntegrationTest(t, testConfig, "TestManifestFetch")

	// Skiping: this test runs the manifest command and then
	// checks that the database records were properly changed, and
	// therefore it's impossible to separate these tests from the
	// service/database.

	SkipConvey("With a SimpleRegistry and test project file", t, func() {

		configPath := filepath.Join(testutil.GetDirectoryOfFile(), "testdata", "manifest", "mongodb-mongo-master.yml")
		modelData, err := modelutil.SetupAPITestData(testConfig, "test", "rhel55", configPath, modelutil.NoPatch)
		require.NoError(t, err, "failed to setup test data")

		taskConfig, err := agentutil.MakeTaskConfigFromModelData(testConfig, modelData)
		require.NoError(t, err)
		logger, err := comm.GetLoggerProducer(ctx, client.TaskData{ID: taskConfig.Task.Id, Secret: taskConfig.Task.Secret}, nil)
		So(err, ShouldBeNil)

		Convey("the manifest load command should execute successfully", func() {
			for _, task := range taskConfig.Project.Tasks {
				So(len(task.Commands), ShouldNotEqual, 0)
				for _, command := range task.Commands {
					pluginCmds, err := Render(command, taskConfig.Project)
					require.NoError(t, err, "Couldn't get plugin command: %s", command.Command)
					So(pluginCmds, ShouldNotBeNil)
					So(err, ShouldBeNil)

					err = pluginCmds[0].Execute(ctx, comm, logger, taskConfig)
					So(err, ShouldBeNil)
				}

			}
			Convey("the manifest should be inserted properly into the database", func() {
				currentManifest, err := manifest.FindOne(manifest.ById(taskConfig.Task.Version))
				So(err, ShouldBeNil)
				So(currentManifest, ShouldNotBeNil)
				So(currentManifest.ProjectName, ShouldEqual, taskConfig.ProjectRef.Id)
				So(currentManifest.Modules, ShouldNotBeNil)
				So(len(currentManifest.Modules), ShouldEqual, 1)
				for key := range currentManifest.Modules {
					So(key, ShouldEqual, "sample")
				}
				So(taskConfig.Expansions.Get("sample_rev"), ShouldEqual, "3c7bfeb82d492dc453e7431be664539c35b5db4b")
			})
		})

		Convey("with a manifest already in the database the manifest should not create a new manifest", func() {
			for _, task := range taskConfig.Project.Tasks {
				So(len(task.Commands), ShouldNotEqual, 0)
				for _, command := range task.Commands {
					pluginCmds, err := Render(command, taskConfig.Project)
					require.NoError(t, err, "Couldn't get plugin command: %s", command.Command)
					So(pluginCmds, ShouldNotBeNil)
					So(err, ShouldBeNil)
					err = pluginCmds[0].Execute(ctx, comm, logger, taskConfig)
					So(err, ShouldBeNil)
				}

			}
			Convey("the manifest should be inserted properly into the database", func() {
				currentManifest, err := manifest.FindOne(manifest.ById(taskConfig.Task.Version))
				So(err, ShouldBeNil)
				So(currentManifest, ShouldNotBeNil)
				So(currentManifest.ProjectName, ShouldEqual, taskConfig.ProjectRef.Id)
				So(currentManifest.Modules, ShouldNotBeNil)
				So(len(currentManifest.Modules), ShouldEqual, 1)
				for key := range currentManifest.Modules {
					So(key, ShouldEqual, "sample")
				}
				So(currentManifest.Modules["sample"].Repo, ShouldEqual, "sample")
				So(taskConfig.Expansions.Get("sample_rev"), ShouldEqual, "3c7bfeb82d492dc453e7431be664539c35b5db4b")
				So(currentManifest.Id, ShouldEqual, taskConfig.Task.Version)
			})
		})
	})
}
