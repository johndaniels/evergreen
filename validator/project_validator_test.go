package validator

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/evergreen-ci/evergreen"
	"github.com/evergreen-ci/evergreen/db"
	mgobson "github.com/evergreen-ci/evergreen/db/mgo/bson"
	"github.com/evergreen-ci/evergreen/model"
	"github.com/evergreen-ci/evergreen/model/distro"
	"github.com/evergreen-ci/evergreen/model/patch"
	_ "github.com/evergreen-ci/evergreen/plugin"
	"github.com/evergreen-ci/utility"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func TestValidateTaskDependencies(t *testing.T) {
	Convey("When validating a project's dependencies", t, func() {
		Convey("if any task has a duplicate dependency, an error should be returned", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{
						Name:      "compile",
						DependsOn: []model.TaskUnitDependency{},
					},
					{
						Name: "testOne",
						DependsOn: []model.TaskUnitDependency{
							{Name: "compile"},
							{Name: "compile"},
						},
					},
				},
			}
			errs := validateTaskDependencies(project)
			So(errs, ShouldNotResemble, ValidationErrors{})
			So(len(errs), ShouldEqual, 1)
		})

		Convey("no error should be returned for dependencies of the same task on two variants", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{
						Name:      "compile",
						DependsOn: []model.TaskUnitDependency{},
					},
					{
						Name: "testOne",
						DependsOn: []model.TaskUnitDependency{
							{Name: "compile", Variant: "v1"},
							{Name: "compile", Variant: "v2"},
						},
					},
				},
				BuildVariants: []model.BuildVariant{
					{Name: "v1"},
					{Name: "v2"},
				},
			}
			So(validateTaskDependencies(project), ShouldResemble, ValidationErrors{})
		})

		Convey("if any dependencies have an invalid name field, an error should be returned", func() {

			project := &model.Project{
				Tasks: []model.ProjectTask{
					{
						Name:      "compile",
						DependsOn: []model.TaskUnitDependency{},
					},
					{
						Name:      "testOne",
						DependsOn: []model.TaskUnitDependency{{Name: "bad"}},
					},
				},
			}
			errs := validateTaskDependencies(project)
			So(errs, ShouldNotResemble, ValidationErrors{})
			So(len(errs), ShouldEqual, 1)
		})
		Convey("if any dependencies have an invalid variant field, an error should be returned", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{
						Name: "compile",
					},
					{
						Name: "testOne",
						DependsOn: []model.TaskUnitDependency{{
							Name:    "compile",
							Variant: "nonexistent",
						}},
					},
				},
			}
			errs := validateTaskDependencies(project)
			So(errs, ShouldNotResemble, ValidationErrors{})
			So(len(errs), ShouldEqual, 1)
		})

		Convey("if any dependencies have an invalid status field, an error should be returned", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{
						Name:      "compile",
						DependsOn: []model.TaskUnitDependency{},
					},
					{
						Name:      "testOne",
						DependsOn: []model.TaskUnitDependency{{Name: "compile", Status: "flibbertyjibbit"}},
					},
					{
						Name:      "testTwo",
						DependsOn: []model.TaskUnitDependency{{Name: "compile", Status: evergreen.TaskSucceeded}},
					},
				},
			}
			errs := validateTaskDependencies(project)
			So(errs, ShouldNotResemble, ValidationErrors{})
			So(len(errs), ShouldEqual, 1)
		})

		Convey("if the dependencies are well-formed, no error should be returned", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{
						Name:      "compile",
						DependsOn: []model.TaskUnitDependency{},
					},
					{
						Name:      "testOne",
						DependsOn: []model.TaskUnitDependency{{Name: "compile"}},
					},
					{
						Name:      "testTwo",
						DependsOn: []model.TaskUnitDependency{{Name: "compile"}},
					},
				},
			}
			So(validateTaskDependencies(project), ShouldResemble, ValidationErrors{})
		})
		Convey("hiding a nonexistent dependency in a task group is found", func() {
			p := &model.Project{
				Tasks: []model.ProjectTask{
					{Name: "1"},
					{Name: "2"},
					{Name: "3", DependsOn: []model.TaskUnitDependency{{Name: "nonexistent"}}},
				},
				TaskGroups: []model.TaskGroup{
					{Name: "tg", Tasks: []string{"3"}},
				},
				BuildVariants: []model.BuildVariant{
					{Name: "v1", Tasks: []model.BuildVariantTaskUnit{{Name: "1"}, {Name: "2"}, {Name: "tg", IsGroup: true}}},
				},
			}
			So(validateTaskDependencies(p)[0].Message, ShouldResemble, "non-existent task name 'nonexistent' in dependencies for task '3'")
		})
		Convey("depending on a non-patchable task should generate a warning", func() {
			p := model.Project{
				Tasks: []model.ProjectTask{
					{Name: "t1", DependsOn: []model.TaskUnitDependency{
						{Name: "t2", Variant: model.AllVariants},
					}},
					{Name: "t2", Patchable: utility.FalsePtr()},
				},
			}
			allTasks := p.FindAllTasksMap()
			errs := checkTaskDependencies(&p.Tasks[0], allTasks)
			errs = append(errs, checkTaskDependencies(&p.Tasks[1], allTasks)...)
			So(len(errs), ShouldEqual, 1)
			So(errs[0].Level, ShouldEqual, Warning)
			So(errs[0].Message, ShouldEqual, "Task 't1' depends on non-patchable task 't2'. Neither will run in patches")
		})
	})
}

func TestValidateDependencyGraph(t *testing.T) {
	Convey("When checking a project's dependency graph", t, func() {
		Convey("cycles in the dependency graph should cause error to be returned", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "bv",
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name:      "compile",
								DependsOn: []model.TaskUnitDependency{{Name: "testOne"}},
							},
							{
								Name:      "testOne",
								DependsOn: []model.TaskUnitDependency{{Name: "compile"}},
							},
							{
								Name:      "testTwo",
								DependsOn: []model.TaskUnitDependency{{Name: "compile"}},
							},
						},
					},
				},
			}
			errs := validateDependencyGraph(project)
			So(errs, ShouldNotResemble, ValidationErrors{})
			So(len(errs), ShouldEqual, 1)
		})

		Convey("task wildcard cycles in the dependency graph should return an error", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "bv",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
							{
								Name:      "testOne",
								DependsOn: []model.TaskUnitDependency{{Name: "compile"}, {Name: "testTwo"}},
							},
							{
								Name:      "testTwo",
								DependsOn: []model.TaskUnitDependency{{Name: model.AllDependencies}},
							},
						},
					},
				},
			}

			errs := validateDependencyGraph(project)
			So(errs, ShouldNotResemble, ValidationErrors{})
			So(len(errs), ShouldEqual, 1)
		})

		Convey("cross-variant cycles in the dependency graph should return an error", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "bv1",
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name: "compile",
							},
							{
								Name: "testOne",
								DependsOn: []model.TaskUnitDependency{
									{Name: "compile"},
									{Name: "testSpecial", Variant: "bv2"},
								},
							}},
					},
					{
						Name: "bv2",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "testSpecial", DependsOn: []model.TaskUnitDependency{{Name: "testOne", Variant: "bv1"}}}},
					},
				},
			}

			errs := validateDependencyGraph(project)
			So(errs, ShouldNotResemble, ValidationErrors{})
			So(len(errs), ShouldEqual, 1)
		})

		Convey("variant wildcard cycles in the dependency graph should return an error", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "bv1",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
							{Name: "testOne", DependsOn: []model.TaskUnitDependency{
								{Name: "compile"},
								{Name: "testSpecial", Variant: "bv2"},
							}}},
					},
					{
						Name: "bv2",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "testSpecial", DependsOn: []model.TaskUnitDependency{{Name: "testOne", Variant: model.AllVariants}}},
						},
					},
					{
						Name: "bv3",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
							{Name: "testOne", DependsOn: []model.TaskUnitDependency{
								{Name: "compile"},
								{Name: "testSpecial", Variant: "bv2"},
							}}},
					},
					{
						Name: "bv4",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
							{Name: "testOne", DependsOn: []model.TaskUnitDependency{
								{Name: "compile"},
								{Name: "testSpecial", Variant: "bv2"},
							}}},
					},
				},
			}

			errs := validateDependencyGraph(project)
			So(errs, ShouldNotResemble, ValidationErrors{})
			So(len(errs), ShouldEqual, 1)
		})

		Convey("cycles in a ** dependency graph should return an error", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "bv1",
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name: "compile",
							},
							{
								Name: "testOne",
								DependsOn: []model.TaskUnitDependency{
									{Name: "compile", Variant: model.AllVariants},
									{Name: "testTwo"},
								},
							}},
					},
					{
						Name: "bv2",
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name: "compile",
							},
							{
								Name: "testOne",
								DependsOn: []model.TaskUnitDependency{
									{Name: "compile", Variant: model.AllVariants},
									{Name: "testTwo"},
								},
							},
							{
								Name: "testTwo",
								DependsOn: []model.TaskUnitDependency{
									{Name: model.AllDependencies, Variant: model.AllVariants},
								},
							},
						},
					},
				},
			}

			errs := validateDependencyGraph(project)
			So(errs, ShouldNotResemble, ValidationErrors{})
			So(len(errs), ShouldEqual, 1)
		})

		Convey("if any task has itself as a dependency, no error should be"+
			" returned", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "bv",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
							{Name: "testOne", DependsOn: []model.TaskUnitDependency{{Name: "testOne"}}},
						},
					},
				},
			}
			So(validateDependencyGraph(project), ShouldResemble, ValidationErrors{})
		})

		Convey("if there is no cycle in the dependency graph, no error should"+
			" be returned", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "bv",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
							{Name: "testOne", DependsOn: []model.TaskUnitDependency{{Name: "compile"}}},
							{Name: "testTwo", DependsOn: []model.TaskUnitDependency{{Name: "compile"}}}},
					},
				},
			}
			So(validateDependencyGraph(project), ShouldResemble, ValidationErrors{})
		})

		Convey("if there is no cycle in the cross-variant dependency graph, no error should"+
			" be returned", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "bv1",
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name: "testOne",
								DependsOn: []model.TaskUnitDependency{
									{Name: "compile", Variant: "bv2"},
								},
							},
						},
					},
					{
						Name: "bv2",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
							{
								Name: "testSpecial",
								DependsOn: []model.TaskUnitDependency{
									{Name: "compile"},
									{Name: "testOne", Variant: "bv1"}},
							},
						},
					},
				},
			}

			So(validateDependencyGraph(project), ShouldResemble, ValidationErrors{})
		})

		Convey("if there is no cycle in the * dependency graph, no error should be returned", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "bv1",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
							{Name: "testOne", DependsOn: []model.TaskUnitDependency{
								{Name: "compile", Variant: model.AllVariants},
							}},
						},
					},
					{
						Name: "bv2",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
							{Name: "testTwo", DependsOn: []model.TaskUnitDependency{
								{Name: model.AllDependencies},
							}},
						},
					},
				},
			}

			So(validateDependencyGraph(project), ShouldResemble, ValidationErrors{})
		})

		Convey("if there is no cycle in the ** dependency graph, no error should be returned", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "bv1",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
							{Name: "testOne", DependsOn: []model.TaskUnitDependency{
								{Name: "compile", Variant: model.AllVariants},
							}},
						},
					},
					{
						Name: "bv2",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
							{Name: "testOne", DependsOn: []model.TaskUnitDependency{
								{Name: "compile", Variant: model.AllVariants},
							}},
							{Name: "testTwo", DependsOn: []model.TaskUnitDependency{
								{Name: model.AllDependencies, Variant: model.AllVariants}},
							}},
					},
				},
			}

			So(validateDependencyGraph(project), ShouldResemble, ValidationErrors{})
		})

	})
}

func TestCheckTaskRuns(t *testing.T) {
	makeProject := func() *model.Project {
		return &model.Project{
			Tasks: []model.ProjectTask{
				{
					Name: "task",
				},
			},
			BuildVariants: []model.BuildVariant{
				{
					Name: "bv",
					Tasks: []model.BuildVariantTaskUnit{
						{Name: "task"},
					},
				},
			},
		}
	}
	Convey("When a task is patchable, not patch-only, and not git-tag-only, no error should be thrown", t, func() {
		project := makeProject()
		project.BuildVariants[0].Tasks[0].Patchable = utility.TruePtr()
		project.BuildVariants[0].Tasks[0].PatchOnly = utility.FalsePtr()
		project.BuildVariants[0].Tasks[0].GitTagOnly = utility.FalsePtr()
		So(len(checkTaskRuns(project)), ShouldEqual, 0)
	})
	Convey("When a task is not patchable, no error should be thrown", t, func() {
		project := makeProject()
		project.BuildVariants[0].Tasks[0].Patchable = utility.FalsePtr()
		So(len(checkTaskRuns(project)), ShouldEqual, 0)
	})
	Convey("When a task is patch-only, no error should be thrown", t, func() {
		project := makeProject()
		project.BuildVariants[0].Tasks[0].PatchOnly = utility.TruePtr()
		So(len(checkTaskRuns(project)), ShouldEqual, 0)
	})
	Convey("When a task is git-tag-only, no error should be thrown", t, func() {
		project := makeProject()
		project.BuildVariants[0].Tasks[0].GitTagOnly = utility.TruePtr()
		So(len(checkTaskRuns(project)), ShouldEqual, 0)
	})
	Convey("When a task is not patchable and not patch-only, no error should be thrown", t, func() {
		project := makeProject()
		project.BuildVariants[0].Tasks[0].Patchable = utility.FalsePtr()
		project.BuildVariants[0].Tasks[0].PatchOnly = utility.FalsePtr()
	})
	Convey("When a task is not patchable and patch-only, an error should be thrown", t, func() {
		project := makeProject()
		project.BuildVariants[0].Tasks[0].Patchable = utility.FalsePtr()
		project.BuildVariants[0].Tasks[0].PatchOnly = utility.TruePtr()
		So(len(checkTaskRuns(project)), ShouldEqual, 1)
	})
	Convey("When a task is patchable and git-tag-only, an error should be thrown", t, func() {
		project := makeProject()
		project.BuildVariants[0].Tasks[0].Patchable = utility.TruePtr()
		project.BuildVariants[0].Tasks[0].GitTagOnly = utility.TruePtr()
		So(len(checkTaskRuns(project)), ShouldEqual, 1)
	})
	Convey("When a task is patch-only and git-tag-only, an error should be thrown", t, func() {
		project := makeProject()
		project.BuildVariants[0].Tasks[0].PatchOnly = utility.TruePtr()
		project.BuildVariants[0].Tasks[0].GitTagOnly = utility.TruePtr()
		So(len(checkTaskRuns(project)), ShouldEqual, 1)
	})
	Convey("When a task is not allowed for git tags and git-tag-only, an error should be thrown", t, func() {
		project := makeProject()
		project.BuildVariants[0].Tasks[0].AllowForGitTag = utility.FalsePtr()
		project.BuildVariants[0].Tasks[0].GitTagOnly = utility.TruePtr()
		So(len(checkTaskRuns(project)), ShouldEqual, 1)
	})
}

func TestValidateTaskNames(t *testing.T) {
	Convey("When a task name contains unauthorized characters, an error should be returned", t, func() {
		project := &model.Project{
			Tasks: []model.ProjectTask{
				{Name: "task|"},
				{Name: "|task"},
				{Name: "ta|sk"},
				{Name: "this is my task"},
				{Name: "task"},
			},
		}
		validationResults := validateTaskNames(project)
		So(len(validationResults), ShouldEqual, 4)
	})
	Convey("A warning should be returned when a task name", t, func() {
		Convey("Contains commas", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{{Name: "task,"}},
			}
			errs := checkTasks(project)
			So(len(errs), ShouldEqual, 3)
			assert.Contains(t, errs.String(), "task name 'task,' should not contain commas")
		})
		Convey("Is the same as the all-dependencies syntax", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{{Name: model.AllDependencies}},
			}
			errs := checkTasks(project)
			So(len(errs), ShouldEqual, 3)
			assert.Contains(t, errs.String(), "task should not be named '*' because it is ambiguous with the all-dependencies '*' specification")
		})
		Convey("Is 'all'", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{{Name: "all"}},
			}
			errs := checkTasks(project)
			So(len(errs), ShouldEqual, 3)
			assert.Contains(t, errs.String(), "task should not be named 'all' because it is ambiguous in task specifications for patches")
		})
	})
}

func TestCheckModules(t *testing.T) {
	Convey("When validating a project's modules", t, func() {
		Convey("An error should be returned when more than one module shares the same name or is empty", func() {
			project := &model.Project{
				Modules: model.ModuleList{
					model.Module{
						Name:   "module-0",
						Branch: "main",
						Repo:   "git@github.com:evergreen-ci/evergreen.git",
					},
					model.Module{
						Name:   "module-0",
						Branch: "main",
						Repo:   "git@github.com:evergreen-ci/evergreen.git",
					},
					model.Module{
						Name:   "module-1",
						Branch: "main",
						Repo:   "git@github.com:evergreen-ci/evergreen.git",
					},
					model.Module{
						Name:   "module-2",
						Branch: "main",
						Repo:   "git@github.com:evergreen-ci/evergreen.git",
					},
					model.Module{
						Name:   "module-1",
						Branch: "main",
						Repo:   "git@github.com:evergreen-ci/evergreen.git",
					},
					model.Module{
						Branch: "main",
						Repo:   "git@github.com:evergreen-ci/evergreen.git",
					},
				},
			}
			So(len(checkModules(project)), ShouldEqual, 3)
		})

		Convey("An error should be returned when the module does not have a branch", func() {
			project := &model.Project{
				Modules: model.ModuleList{
					model.Module{
						Name: "module-0",
						Repo: "git@github.com:evergreen-ci/evergreen.git",
					},
					model.Module{
						Name:   "module-1",
						Branch: "main",
						Repo:   "git@github.com:evergreen-ci/evergreen.git",
					},
					model.Module{
						Name: "module-2",
						Repo: "git@github.com:evergreen-ci/evergreen.git",
					},
				},
			}
			So(len(checkModules(project)), ShouldEqual, 2)
		})

		Convey("An error should be returned when the module's repo is empty or invalid", func() {
			project := &model.Project{
				Modules: model.ModuleList{
					model.Module{
						Name:   "module-0",
						Branch: "main",
						Repo:   "git@github.com:evergreen-ci/evergreen.git",
					},
					model.Module{
						Name:   "module-1",
						Branch: "main",
					},
					model.Module{
						Name:   "module-2",
						Branch: "main",
						Repo:   "evergreen-ci/evergreen.git",
					},
					model.Module{
						Name:   "module-3",
						Branch: "main",
						Repo:   "git@github.com:/evergreen.git",
					},
					model.Module{
						Name:   "module-4",
						Branch: "main",
						Repo:   "git@github.com:evergreen-ci/.git",
					},
				},
			}
			So(len(checkModules(project)), ShouldEqual, 4)
		})
	})
}

func TestValidateBVNames(t *testing.T) {
	Convey("When validating a project's build variants' names", t, func() {
		Convey("if any variant has a duplicate entry, an error should be returned", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{Name: "linux"},
					{Name: "linux"},
				},
			}
			validationResults := validateBVNames(project)
			So(validationResults, ShouldNotResemble, ValidationErrors{})
			So(len(validationResults), ShouldEqual, 3)
			So(validationResults[0].Level, ShouldEqual, Error)
		})

		Convey("if two variants have the same display name, a warning should be returned, but no errors", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{Name: "linux1", DisplayName: "foo"},
					{Name: "linux", DisplayName: "foo"},
				},
			}

			validationResults := checkBuildVariants(project)
			numErrors := len(validationResults.AtLevel(Error))
			numWarnings := len(validationResults.AtLevel(Warning))

			So(numWarnings, ShouldEqual, 3)
			So(numErrors, ShouldEqual, 0)
			So(len(validationResults), ShouldEqual, 3)
		})

		Convey("if several buildvariants have duplicate entries, all errors "+
			"should be returned", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{Name: "linux", DisplayName: "foo0"},
					{Name: "linux", DisplayName: "foo1"},
					{Name: "windows", DisplayName: "foo2"},
					{Name: "windows", DisplayName: "foo3"},
				},
			}
			So(validateBVNames(project), ShouldNotResemble, ValidationErrors{})
			So(len(validateBVNames(project)), ShouldEqual, 2)
		})

		Convey("if no buildvariants have duplicate entries, no error should be"+
			" returned", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{Name: "linux", DisplayName: "foo0"},
					{Name: "windows", DisplayName: "foo1"},
				},
			}
			So(validateBVNames(project), ShouldResemble, ValidationErrors{})
		})

		Convey("if a buildvariant name contains unauthorized characters, an error should be returned", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{Name: "|linux", DisplayName: "foo0"},
					{Name: "linux|", DisplayName: "foo1"},
					{Name: "wind|ows", DisplayName: "foo2"},
					{Name: "windows", DisplayName: "foo3"},
				},
			}
			So(validateBVNames(project), ShouldNotResemble, ValidationErrors{})
			So(len(validateBVNames(project)), ShouldEqual, 3)
		})
		Convey("A warning should be returned when a buildvariant name", func() {
			Convey("Contains commas", func() {
				project := &model.Project{
					BuildVariants: []model.BuildVariant{
						{Name: "variant,", DisplayName: "display_name"},
					},
				}
				buildVariant := project.BuildVariants[0]
				So(len(checkBVNames(&buildVariant)), ShouldEqual, 1)
			})
			Convey("Is the same as the all-dependencies syntax", func() {
				project := &model.Project{
					BuildVariants: []model.BuildVariant{
						{Name: model.AllVariants, DisplayName: "display_name"},
					},
				}
				buildVariant := project.BuildVariants[0]
				So(len(checkBVNames(&buildVariant)), ShouldEqual, 1)
			})
			Convey("Is 'all'", func() {
				project := &model.Project{
					BuildVariants: []model.BuildVariant{{Name: "all", DisplayName: "display_name"}},
				}
				buildVariant := project.BuildVariants[0]
				So(len(checkBVNames(&buildVariant)), ShouldEqual, 1)
			})
		})
	})
}

func TestValidateBVTaskNames(t *testing.T) {
	Convey("When validating a project's build variant's task names", t, func() {
		Convey("if any task has a duplicate entry, an error should be"+
			" returned", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "linux",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
							{Name: "compile"},
						},
					},
				},
			}
			So(validateBVTaskNames(project), ShouldNotResemble, ValidationErrors{})
			So(len(validateBVTaskNames(project)), ShouldEqual, 1)
		})

		Convey("if several task have duplicate entries, all errors should be"+
			" returned", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "linux",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
							{Name: "compile"},
							{Name: "test"},
							{Name: "test"},
						},
					},
				},
			}
			So(validateBVTaskNames(project), ShouldNotResemble, ValidationErrors{})
			So(len(validateBVTaskNames(project)), ShouldEqual, 2)
		})

		Convey("if no tasks have duplicate entries, no error should be"+
			" returned", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "linux",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
							{Name: "test"},
						},
					},
				},
			}
			So(validateBVTaskNames(project), ShouldResemble, ValidationErrors{})
		})
	})
}

func TestValidateBVBatchTimes(t *testing.T) {
	batchtime := 126
	p := &model.Project{
		BuildVariants: []model.BuildVariant{
			{
				Name:          "linux",
				BatchTime:     &batchtime,
				CronBatchTime: "@notadescriptor",
			},
		},
	}
	// can't set cron and batchtime for build variants
	assert.Len(t, validateBVBatchTimes(p), 2)

	p.BuildVariants[0].BatchTime = nil
	p.BuildVariants[0].CronBatchTime = "@daily"
	assert.Empty(t, validateBVBatchTimes(p))

	// can have task and variant batchtime set
	p.BuildVariants[0].Tasks = []model.BuildVariantTaskUnit{
		{Name: "t1", BatchTime: &batchtime},
		{Name: "t2"},
	}
	assert.Len(t, validateBVBatchTimes(p), 0)

	// can't set cron and batchtime for tasks
	p.BuildVariants[0].Tasks[0].CronBatchTime = "@daily"
	assert.Len(t, validateBVBatchTimes(p), 1)

	p.BuildVariants[0].Tasks[0].BatchTime = nil
	assert.Len(t, validateBVBatchTimes(p), 0)

	// warning if activated to true with batchtime
	p.BuildVariants[0].Activate = utility.TruePtr()
	bv := p.BuildVariants[0]
	assert.Len(t, checkBVBatchTimes(&bv), 1)

}

func TestCheckBVsContainTasks(t *testing.T) {
	Convey("When validating a project's build variants", t, func() {
		Convey("if any build variant contains no tasks an error should be returned", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "linux",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
						},
					},
					{
						Name:  "windows",
						Tasks: []model.BuildVariantTaskUnit{},
					},
				},
			}
			So(len(checkBuildVariants(project)), ShouldEqual, 2)
		})

		Convey("if all build variants contain tasks no errors should be returned", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "linux",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
						},
					},
					{
						Name: "windows",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
						},
					},
				},
			}
			So(len(checkBuildVariants(project)), ShouldEqual, 1)
		})
	})
}

func TestValidateAllDependenciesSpec(t *testing.T) {
	Convey("When validating a project", t, func() {
		Convey("if a task references all dependencies, no other dependency "+
			"should be specified. If one is, an error should be returned",
			func() {
				project := &model.Project{
					Tasks: []model.ProjectTask{
						{
							Name: "compile",
							DependsOn: []model.TaskUnitDependency{
								{Name: model.AllDependencies},
								{Name: "testOne"},
							},
						},
					},
				}
				So(validateAllDependenciesSpec(project), ShouldNotResemble,
					ValidationErrors{})
				So(len(validateAllDependenciesSpec(project)), ShouldEqual, 1)
			})
		Convey("if a task references only all dependencies, no error should "+
			"be returned", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{
						Name: "compile",
						DependsOn: []model.TaskUnitDependency{
							{Name: model.AllDependencies},
						},
					},
				},
			}
			So(validateAllDependenciesSpec(project), ShouldResemble, ValidationErrors{})
		})
		Convey("if a task references any other dependencies, no error should "+
			"be returned", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{
						Name: "compile",
						DependsOn: []model.TaskUnitDependency{
							{Name: "hello"},
						},
					},
				},
			}
			So(validateAllDependenciesSpec(project), ShouldResemble, ValidationErrors{})
		})
		Convey("if a task references all dependencies on multiple variants, no error should "+
			" be returned", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{
						Name: "coverage",
						DependsOn: []model.TaskUnitDependency{
							{
								Name:    "*",
								Variant: "ubuntu1604",
							},
							{
								Name:    "*",
								Variant: "coverage",
							},
						},
					},
				},
			}
			So(validateAllDependenciesSpec(project), ShouldResemble, ValidationErrors{})
		})
	})
}

func TestValidateProjectTaskNames(t *testing.T) {
	Convey("When validating a project", t, func() {
		Convey("ensure any duplicate task names throw an error", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{Name: "compile"},
					{Name: "compile"},
				},
			}
			So(validateProjectTaskNames(project), ShouldNotResemble, ValidationErrors{})
			So(len(validateProjectTaskNames(project)), ShouldEqual, 1)
		})
		Convey("ensure unique task names do not throw an error", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{Name: "compile"},
				},
			}
			So(validateProjectTaskNames(project), ShouldResemble, ValidationErrors{})
		})
	})
}

func TestValidateProjectTaskIdsAndTags(t *testing.T) {
	Convey("When validating a project", t, func() {
		Convey("ensure bad task tags throw an error", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{Name: "compile", Tags: []string{"a", "!b", "ccc ccc", "d", ".e", "f\tf"}},
				},
			}
			So(validateProjectTaskIdsAndTags(project), ShouldNotResemble, ValidationErrors{})
			So(len(validateProjectTaskIdsAndTags(project)), ShouldEqual, 4)
		})
		Convey("ensure bad task names throw an error", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{Name: "compile"},
					{Name: "!compile"},
					{Name: ".compile"},
					{Name: "Fun!"},
				},
			}
			So(validateProjectTaskIdsAndTags(project), ShouldNotResemble, ValidationErrors{})
			So(len(validateProjectTaskIdsAndTags(project)), ShouldEqual, 2)
		})
	})
}

func TestValidatePeriodicBuilds(t *testing.T) {
	projectConfig := &model.ProjectConfig{
		Id: "project-1",
		ProjectConfigFields: model.ProjectConfigFields{
			PeriodicBuilds: []model.PeriodicBuildDefinition{
				{
					ID:            "so_occasional",
					ConfigFile:    "build.yml",
					IntervalHours: -1,
				},
				{
					ID:            "more_frequent",
					ConfigFile:    "",
					IntervalHours: 1,
				},
			},
		},
	}
	validationErrs := validateProjectConfigPeriodicBuilds(projectConfig)
	assert.Len(t, validationErrs, 2)
	assert.Contains(t, validationErrs[0].Message, "interval must be a positive integer")
	assert.Contains(t, validationErrs[1].Message, "a config file must be specified")
}

func TestValidatePlugins(t *testing.T) {
	assert := assert.New(t)
	require.NoError(t, db.Clear(model.ProjectRefCollection),
		"Error clearing collection")
	projectRef := &model.ProjectRef{
		Enabled: utility.TruePtr(),
		Id:      "p1",
	}
	assert.Nil(projectRef.Insert())
	Convey("When validating a project", t, func() {
		Convey("ensure bad plugin configs throw an error", func() {
			So(validateProjectConfigPlugins(&model.ProjectConfig{Id: "", ProjectConfigFields: model.ProjectConfigFields{BuildBaronSettings: &evergreen.BuildBaronSettings{
				TicketCreateProject:  "BFG",
				TicketSearchProjects: []string{"BF", "BFG"},
			}}}), ShouldResemble, ValidationErrors{})

			So(validateProjectConfigPlugins(&model.ProjectConfig{Id: "", ProjectConfigFields: model.ProjectConfigFields{BuildBaronSettings: &evergreen.BuildBaronSettings{
				TicketCreateProject:  "BFG",
				TicketSearchProjects: []string{"BF", "BFG"},
			}}}), ShouldResemble, ValidationErrors{})

			So(validateProjectConfigPlugins(&model.ProjectConfig{Id: "", ProjectConfigFields: model.ProjectConfigFields{BuildBaronSettings: &evergreen.BuildBaronSettings{
				TicketCreateProject:  "BFG",
				TicketSearchProjects: []string{"BF", "BFG"},
			}}}), ShouldResemble, ValidationErrors{})

			So(validateProjectConfigPlugins(&model.ProjectConfig{Id: "", ProjectConfigFields: model.ProjectConfigFields{BuildBaronSettings: &evergreen.BuildBaronSettings{
				TicketCreateProject: "BFG",
			}}}), ShouldNotBeNil)

			So(validateProjectConfigPlugins(&model.ProjectConfig{Id: "", ProjectConfigFields: model.ProjectConfigFields{BuildBaronSettings: &evergreen.BuildBaronSettings{
				TicketSearchProjects: []string{"BF", "BFG"},
			}}}), ShouldNotBeNil)

			So(validateProjectConfigPlugins(&model.ProjectConfig{Id: "", ProjectConfigFields: model.ProjectConfigFields{BuildBaronSettings: &evergreen.BuildBaronSettings{
				TicketCreateProject:     "BFG",
				TicketSearchProjects:    []string{"BF", "BFG"},
				BFSuggestionServer:      "https://evergreen.mongodb.com",
				BFSuggestionUsername:    "user",
				BFSuggestionPassword:    "pass",
				BFSuggestionTimeoutSecs: 10,
			}}}), ShouldResemble, ValidationErrors{})

			So(validateProjectConfigPlugins(&model.ProjectConfig{Id: "", ProjectConfigFields: model.ProjectConfigFields{BuildBaronSettings: &evergreen.BuildBaronSettings{
				TicketCreateProject:     "BFG",
				TicketSearchProjects:    []string{"BF", "BFG"},
				BFSuggestionServer:      "https://evergreen.mongodb.com",
				BFSuggestionTimeoutSecs: 10,
			}}}), ShouldResemble, ValidationErrors{})

			So(validateProjectConfigPlugins(&model.ProjectConfig{Id: "", ProjectConfigFields: model.ProjectConfigFields{BuildBaronSettings: &evergreen.BuildBaronSettings{
				TicketCreateProject:  "BFG",
				TicketSearchProjects: []string{"BF", "BFG"},
				BFSuggestionUsername: "user",
				BFSuggestionPassword: "pass",
			}}}), ShouldNotBeNil)

			So(validateProjectConfigPlugins(&model.ProjectConfig{Id: "", ProjectConfigFields: model.ProjectConfigFields{BuildBaronSettings: &evergreen.BuildBaronSettings{
				TicketCreateProject:     "BFG",
				TicketSearchProjects:    []string{"BF", "BFG"},
				BFSuggestionTimeoutSecs: 10,
			}}}), ShouldNotBeNil)

			So(validateProjectConfigPlugins(&model.ProjectConfig{Id: "", ProjectConfigFields: model.ProjectConfigFields{BuildBaronSettings: &evergreen.BuildBaronSettings{
				TicketCreateProject:     "BFG",
				TicketSearchProjects:    []string{"BF", "BFG"},
				BFSuggestionServer:      "://evergreen.mongodb.com",
				BFSuggestionTimeoutSecs: 10,
			}}}), ShouldNotBeNil)

			So(validateProjectConfigPlugins(&model.ProjectConfig{Id: "", ProjectConfigFields: model.ProjectConfigFields{BuildBaronSettings: &evergreen.BuildBaronSettings{
				TicketCreateProject:     "BFG",
				TicketSearchProjects:    []string{"BF", "BFG"},
				BFSuggestionServer:      "https://evergreen.mongodb.com",
				BFSuggestionPassword:    "pass",
				BFSuggestionTimeoutSecs: 10,
			}}}), ShouldNotBeNil)
			So(validateProjectConfigPlugins(&model.ProjectConfig{Id: "", ProjectConfigFields: model.ProjectConfigFields{BuildBaronSettings: &evergreen.BuildBaronSettings{
				TicketCreateProject:     "BFG",
				TicketSearchProjects:    []string{"BF", "BFG"},
				BFSuggestionServer:      "https://evergreen.mongodb.com",
				BFSuggestionTimeoutSecs: 0,
			}}}), ShouldNotBeNil)

			So(validateProjectConfigPlugins(&model.ProjectConfig{Id: "", ProjectConfigFields: model.ProjectConfigFields{BuildBaronSettings: &evergreen.BuildBaronSettings{
				TicketCreateProject:     "BFG",
				TicketSearchProjects:    []string{"BF", "BFG"},
				BFSuggestionServer:      "https://evergreen.mongodb.com",
				BFSuggestionTimeoutSecs: -1,
			}}}), ShouldNotBeNil)
		})
	})
}

func TestValidateProjectAliases(t *testing.T) {
	Convey("When validating a project", t, func() {
		Convey("ensure misconfigured aliases throw an error", func() {
			projectConfig := &model.ProjectConfig{
				Id: "project-1",
				ProjectConfigFields: model.ProjectConfigFields{
					PatchAliases: []model.ProjectAlias{
						{
							ID:        mgobson.NewObjectId(),
							ProjectID: "project-1",
							Alias:     "",
							Variant:   "v1",
							Task:      "^test",
						},
						{
							ID:        mgobson.NewObjectId(),
							ProjectID: "project-1",
							Alias:     "alias-1",
							Task:      "^test",
						},
						{
							ID:        mgobson.NewObjectId(),
							ProjectID: "project-1",
							Alias:     "alias-1",
							Variant:   "v1",
						},
						{
							ID:        mgobson.NewObjectId(),
							ProjectID: "project-1",
							Alias:     "alias-1",
							Variant:   "[0-9]++",
							Task:      "^test",
						},
						{
							ID:        mgobson.NewObjectId(),
							ProjectID: "project-1",
							Alias:     "alias-1",
							Variant:   "v1",
							Task:      "[0-9]++",
						},
					},
					CommitQueueAliases: []model.ProjectAlias{
						{
							ID:        mgobson.NewObjectId(),
							ProjectID: "project-1",
							Alias:     evergreen.CommitQueueAlias,
							Variant:   "v1",
							Task:      "^test",
						},
					},
					GitHubChecksAliases: []model.ProjectAlias{
						{
							ID:        mgobson.NewObjectId(),
							ProjectID: "project-1",
							Alias:     evergreen.GithubChecksAlias,
							Variant:   "v1",
							Task:      "^test",
						},
					},
					GitTagAliases: []model.ProjectAlias{
						{
							ID:        mgobson.NewObjectId(),
							ProjectID: "project-1",
							Alias:     evergreen.GitTagAlias,
							Variant:   "v1",
							Task:      "^test",
						},
						{
							ID:        mgobson.NewObjectId(),
							ProjectID: "project-1",
							Alias:     evergreen.GitTagAlias,
							Variant:   "v1",
							Task:      "^test",
							GitTag:    "[0-9]++",
						},
						{
							ID:         mgobson.NewObjectId(),
							ProjectID:  "project-1",
							Alias:      evergreen.GitTagAlias,
							Variant:    "v1",
							Task:       "^test",
							RemotePath: "remote/path",
							GitTag:     "^test",
						},
					},
				},
			}
			validationErrs := validateProjectConfigAliases(projectConfig)
			So(validationErrs, ShouldNotResemble, ValidationErrors{})
			So(len(validationErrs), ShouldEqual, 8)
			So(validationErrs[0].Message, ShouldContainSubstring, "can't be empty string")
			So(validationErrs[1].Message, ShouldContainSubstring, "must specify exactly one of variant regex")
			So(validationErrs[2].Message, ShouldContainSubstring, "must specify exactly one of task regex")
			So(validationErrs[3].Message, ShouldContainSubstring, "variant regex #4 is invalid")
			So(validationErrs[4].Message, ShouldContainSubstring, "task regex #5 is invalid")
			So(validationErrs[5].Message, ShouldContainSubstring, "must define valid git tag regex")
			So(validationErrs[6].Message, ShouldContainSubstring, "git tag regex #2 is invalid")
			So(validationErrs[7].Message, ShouldContainSubstring, "cannot define remote path")
		})
	})
}

func TestCheckTaskCommands(t *testing.T) {
	Convey("When validating a project", t, func() {
		Convey("ensure tasks that do not have at least one command throw "+
			"an error", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{Name: "compile"},
				},
			}
			errs := checkTasks(project)
			So(errs, ShouldNotResemble, ValidationErrors{})
			So(len(errs), ShouldEqual, 2)
			assert.Contains(t, errs.String(), "task 'compile' does not "+
				"contain any commands")
		})
		Convey("ensure tasks that have at least one command do not throw any errors",
			func() {
				project := &model.Project{
					Tasks: []model.ProjectTask{
						{
							Name: "compile",
							Commands: []model.PluginCommandConf{
								{
									Command: "gotest.parse_files",
									Params: map[string]interface{}{
										"files": []interface{}{"test"},
									},
								},
							},
						},
					},
				}
				So(validateProjectTaskNames(project), ShouldResemble, ValidationErrors{})
			})
		Convey("ensure that plugin commands have setup type",
			func() {
				project := &model.Project{
					Tasks: []model.ProjectTask{
						{
							Name: "compile",
							Commands: []model.PluginCommandConf{
								{
									Command: "gotest.parse_files",
									Type:    "setup",
									Params: map[string]interface{}{
										"files": []interface{}{"test"},
									},
								},
							},
						},
					},
				}
				So(validateProjectTaskNames(project), ShouldResemble, ValidationErrors{})
			})
	})
}

func TestEnsureReferentialIntegrity(t *testing.T) {
	Convey("When validating a project", t, func() {
		distroIds := []string{"rhel55"}
		distroAliases := []string{"rhel55-alias"}
		Convey("an error should be thrown if a referenced task for a "+
			"buildvariant does not exist", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{
						Name: "compile",
					},
				},
				BuildVariants: []model.BuildVariant{
					{
						Name: "linux",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "test"},
						},
					},
				},
			}
			errs := ensureReferentialIntegrity(project, nil, distroIds, distroAliases)
			So(errs, ShouldNotResemble, ValidationErrors{})
			So(len(errs), ShouldEqual, 1)
		})

		Convey("no error should be thrown if a referenced task for a "+
			"buildvariant does exist", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{Name: "compile"},
				},
				BuildVariants: []model.BuildVariant{
					{
						Name: "linux",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "compile"},
						},
					},
				},
			}
			So(ensureReferentialIntegrity(project, nil, distroIds, distroAliases), ShouldResemble,
				ValidationErrors{})
		})

		Convey("an error should be thrown if a referenced distro for a "+
			"buildvariant does not exist", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name:  "enterprise",
						RunOn: []string{"hello"},
					},
				},
			}
			errs := ensureReferentialIntegrity(project, nil, distroIds, distroAliases)
			So(errs, ShouldNotResemble,
				ValidationErrors{})
			So(len(errs), ShouldEqual, 1)
		})

		Convey("an error should be thrown if a referenced distro for a "+
			"buildvariant has the same name as an existing container", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name:  "enterprise",
						RunOn: []string{"rhel55"},
					},
				},
			}
			containerNameMap := map[string]bool{
				"rhel55": true,
			}
			errs := ensureReferentialIntegrity(project, containerNameMap, distroIds, distroAliases)
			So(errs, ShouldNotResemble,
				ValidationErrors{})
			So(len(errs), ShouldEqual, 2)
			So(errs[0].Message, ShouldContainSubstring, "buildvariant 'enterprise' references a container name overlapping with an existing distro 'rhel55'")
			So(errs[1].Message, ShouldContainSubstring, "run_on cannot contain a mixture of containers and distros")
		})

		Convey("an error should be thrown if a buildvariant references a mix of distros and containers to run on", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name:  "enterprise",
						RunOn: []string{"rhel55", "c1"},
					},
				},
			}
			containerNameMap := map[string]bool{
				"c1": true,
			}
			errs := ensureReferentialIntegrity(project, containerNameMap, distroIds, distroAliases)
			So(errs, ShouldNotResemble,
				ValidationErrors{})
			So(len(errs), ShouldEqual, 1)
			So(errs[0].Message, ShouldContainSubstring, "run_on cannot contain a mixture of containers and distros")
		})

		Convey("no error should be thrown if a referenced distro ID for a "+
			"buildvariant does exist", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name:  "enterprise",
						RunOn: distroIds,
					},
				},
			}
			So(ensureReferentialIntegrity(project, nil, distroIds, distroAliases), ShouldResemble, ValidationErrors{})
		})

		Convey("no error should be thrown if a referenced distro alias for a"+
			"buildvariant does exist", func() {
			project := &model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name:  "enterprise",
						RunOn: distroAliases,
					},
				},
			}
			So(ensureReferentialIntegrity(project, nil, distroIds, distroAliases), ShouldResemble, ValidationErrors{})
		})
	})
}

func TestValidateProjectConfigContainers(t *testing.T) {
	t.Run("SucceedsWithValidContainers", func(t *testing.T) {
		pc := model.ProjectConfig{
			ProjectConfigFields: model.ProjectConfigFields{
				ContainerSizes: map[string]model.ContainerResources{
					"small": {
						CPU:      128,
						MemoryMB: 128,
					},
					"large": {
						CPU:      2048,
						MemoryMB: 2048,
					},
				},
			},
		}
		errs := validateProjectConfigContainers(&pc)
		assert.Empty(t, errs)
	})
	t.Run("FailsWithInvalidContainerResources", func(t *testing.T) {
		pc := model.ProjectConfig{
			ProjectConfigFields: model.ProjectConfigFields{
				ContainerSizes: map[string]model.ContainerResources{
					"invalid": {
						CPU:      -10,
						MemoryMB: -5,
					},
				},
			},
		}
		errs := validateProjectConfigContainers(&pc)
		assert.NotEmpty(t, errs)
	})
	t.Run("FailsWithUnnamedContainerSize", func(t *testing.T) {
		pc := model.ProjectConfig{
			ProjectConfigFields: model.ProjectConfigFields{
				ContainerSizes: map[string]model.ContainerResources{
					"": {
						CPU:      128,
						MemoryMB: 128,
					},
				},
			},
		}
		errs := validateProjectConfigContainers(&pc)
		assert.NotEmpty(t, errs)
	})
}

func TestValidatePluginCommands(t *testing.T) {
	Convey("When validating a project", t, func() {
		Convey("an error should be thrown if a referenced plugin for a task does not exist", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{
						Name: "compile",
						Commands: []model.PluginCommandConf{
							{
								Function: "",
								Command:  "a.b",
								Params:   map[string]interface{}{},
							},
						},
					},
				},
			}
			So(validatePluginCommands(project), ShouldNotResemble, ValidationErrors{})
			So(len(validatePluginCommands(project)), ShouldEqual, 1)
		})
		Convey("an error should be thrown if a referenced function command is invalid (invalid params)", func() {
			project := &model.Project{
				Functions: map[string]*model.YAMLCommandSet{
					"funcOne": {
						SingleCommand: &model.PluginCommandConf{
							Command: "gotest.parse_files",
							Params: map[string]interface{}{
								"blah": []interface{}{"test"},
							},
						},
					},
				},
			}
			So(validatePluginCommands(project), ShouldNotResemble, ValidationErrors{})
			So(len(validatePluginCommands(project)), ShouldEqual, 1)
		})
		Convey("an error should be thrown if a shell.exec command has misspelled params", func() {
			exampleYml := `
tasks:
- name: example_task
  exec_timeout_secs: 100
  commands:
  - command: shell.exec
    parms:
      script: echo test
`
			proj := model.Project{}
			ctx := context.Background()
			pp, err := model.LoadProjectInto(ctx, []byte(exampleYml), nil, "example_project", &proj)
			So(pp, ShouldNotBeNil)
			So(proj, ShouldNotBeNil)
			So(err, ShouldBeNil)
			validationErrs := validatePluginCommands(&proj)
			So(validationErrs, ShouldNotResemble, ValidationErrors{})
			So(len(validationErrs.AtLevel(Error)), ShouldEqual, 1)
			So(validationErrs.AtLevel(Error)[0].Message, ShouldContainSubstring, "params cannot be nil")
		})
		Convey("an warning should return if a shell.exec command is missing a script", func() {
			project := &model.Project{
				Functions: map[string]*model.YAMLCommandSet{
					"funcOne": {
						SingleCommand: &model.PluginCommandConf{
							Command: "shell.exec",
							Type:    "system",
							Params: map[string]interface{}{
								"files": []interface{}{"test"},
							},
						},
					},
				},
			}
			validationErrs := validatePluginCommands(project)
			So(validationErrs, ShouldNotResemble, ValidationErrors{})
			So(len(validationErrs.AtLevel(Warning)), ShouldEqual, 1)
			So(validationErrs.AtLevel(Warning)[0].Message, ShouldContainSubstring, "specified without a script")
		})
		Convey("an error should not be thrown if a shell.exec command is defined with a script", func() {
			project := &model.Project{
				Functions: map[string]*model.YAMLCommandSet{
					"funcOne": {
						SingleCommand: &model.PluginCommandConf{
							Command: "shell.exec",
							Type:    "system",
							Params: map[string]interface{}{
								"script": "echo hi",
							},
						},
					},
				},
			}
			validationErrs := validatePluginCommands(project)
			So(validationErrs, ShouldResemble, ValidationErrors{})
			So(len(validationErrs.AtLevel(Error)), ShouldEqual, 0)
		})
		Convey("an error should be thrown if a shell.exec command is missing params", func() {
			project := &model.Project{
				Functions: map[string]*model.YAMLCommandSet{
					"funcOne": {
						SingleCommand: &model.PluginCommandConf{
							Command: "shell.exec",
							Type:    "system",
						},
					},
				},
			}
			validationErrs := validatePluginCommands(project)
			So(validationErrs, ShouldNotResemble, ValidationErrors{})
			So(len(validationErrs.AtLevel(Error)), ShouldEqual, 1)
			So(validationErrs.AtLevel(Error)[0].Message, ShouldContainSubstring, "params cannot be nil")
		})
		Convey("an error should be thrown if both a function and a plugin command are referenced", func() {
			project := &model.Project{
				Functions: map[string]*model.YAMLCommandSet{
					"funcOne": {
						SingleCommand: &model.PluginCommandConf{
							Command: "gotest.parse_files",
							Params: map[string]interface{}{
								"files": []interface{}{"test"},
							},
						},
					},
				},
				Tasks: []model.ProjectTask{
					{
						Name: "compile",
						Commands: []model.PluginCommandConf{
							{
								Function: "funcOne",
								Command:  "gotest.parse_files",
								Params: map[string]interface{}{
									"files": []interface{}{"test"},
								},
							},
						},
					},
				},
			}
			errs := validatePluginCommands(project)
			So(errs, ShouldNotResemble, ValidationErrors{})
			So(len(errs), ShouldEqual, 1)
		})
		Convey("an error should be thrown if a function plugin command doesn't have commands", func() {
			project := &model.Project{
				Functions: map[string]*model.YAMLCommandSet{
					"funcOne": {
						SingleCommand: &model.PluginCommandConf{
							Params: map[string]interface{}{
								"blah": []interface{}{"test"},
							},
						},
					},
				},
			}
			So(validatePluginCommands(project), ShouldNotResemble, ValidationErrors{})
			So(len(validatePluginCommands(project)), ShouldEqual, 1)
		})
		Convey("no error should be thrown if a function plugin command is valid", func() {
			project := &model.Project{
				Functions: map[string]*model.YAMLCommandSet{
					"funcOne": {
						SingleCommand: &model.PluginCommandConf{
							Command: "gotest.parse_files",
							Params: map[string]interface{}{
								"files": []interface{}{"test"},
							},
						},
					},
				},
			}
			So(validatePluginCommands(project), ShouldResemble, ValidationErrors{})
		})
		Convey("an error should be thrown if a function 'a' references "+
			"any another function", func() {
			project := &model.Project{
				Functions: map[string]*model.YAMLCommandSet{
					"a": {
						SingleCommand: &model.PluginCommandConf{
							Function: "b",
							Command:  "gotest.parse_files",
							Params: map[string]interface{}{
								"files": []interface{}{"test"},
							},
						},
					},
					"b": {
						SingleCommand: &model.PluginCommandConf{
							Command: "gotest.parse_files",
							Params: map[string]interface{}{
								"files": []interface{}{"test"},
							},
						},
					},
				},
			}
			So(validatePluginCommands(project), ShouldNotResemble, ValidationErrors{})
			So(len(validatePluginCommands(project)), ShouldEqual, 2)
		})
		Convey("errors should be thrown if a function 'a' references "+
			"another function, 'b', which that does not exist", func() {
			project := &model.Project{
				Functions: map[string]*model.YAMLCommandSet{
					"a": {
						SingleCommand: &model.PluginCommandConf{
							Function: "b",
							Command:  "gotest.parse_files",
							Params: map[string]interface{}{
								"files": []interface{}{"test"},
							},
						},
					},
				},
			}
			So(validatePluginCommands(project), ShouldNotResemble, ValidationErrors{})
			So(len(validatePluginCommands(project)), ShouldEqual, 3)
		})

		Convey("an error should be thrown if a referenced pre plugin command is invalid", func() {
			project := &model.Project{
				Pre: &model.YAMLCommandSet{
					MultiCommand: []model.PluginCommandConf{
						{
							Command: "gotest.parse_files",
							Params:  map[string]interface{}{},
						},
					},
				},
			}
			So(validatePluginCommands(project), ShouldNotResemble, ValidationErrors{})
			So(len(validatePluginCommands(project)), ShouldEqual, 1)
		})
		Convey("no error should be thrown if a referenced pre plugin command is valid", func() {
			project := &model.Project{
				Pre: &model.YAMLCommandSet{
					MultiCommand: []model.PluginCommandConf{
						{
							Function: "",
							Command:  "gotest.parse_files",
							Params: map[string]interface{}{
								"files": []interface{}{"test"},
							},
						},
					},
				},
			}
			So(validatePluginCommands(project), ShouldResemble, ValidationErrors{})
		})
		Convey("an error should be thrown if a referenced post plugin command is invalid", func() {
			project := &model.Project{
				Post: &model.YAMLCommandSet{
					MultiCommand: []model.PluginCommandConf{
						{
							Function: "",
							Command:  "gotest.parse_files",
							Params:   map[string]interface{}{},
						},
					},
				},
			}
			So(validatePluginCommands(project), ShouldNotResemble, ValidationErrors{})
			So(len(validatePluginCommands(project)), ShouldEqual, 1)
		})
		Convey("no error should be thrown if a referenced post plugin command is valid", func() {
			project := &model.Project{
				Post: &model.YAMLCommandSet{
					MultiCommand: []model.PluginCommandConf{
						{
							Function: "",
							Command:  "gotest.parse_files",
							Params: map[string]interface{}{
								"files": []interface{}{"test"},
							},
						},
					},
				},
			}
			So(validatePluginCommands(project), ShouldResemble, ValidationErrors{})
		})
		Convey("an error should be thrown if a referenced timeout plugin command is invalid", func() {
			project := &model.Project{
				Timeout: &model.YAMLCommandSet{
					MultiCommand: []model.PluginCommandConf{
						{
							Function: "",
							Command:  "gotest.parse_files",
							Params:   map[string]interface{}{},
						},
					},
				},
			}
			So(validatePluginCommands(project), ShouldNotResemble, ValidationErrors{})
			So(len(validatePluginCommands(project)), ShouldEqual, 1)
		})
		Convey("no error should be thrown if a referenced timeout plugin command is valid", func() {
			project := &model.Project{
				Timeout: &model.YAMLCommandSet{
					MultiCommand: []model.PluginCommandConf{
						{
							Function: "",
							Command:  "gotest.parse_files",
							Params: map[string]interface{}{
								"files": []interface{}{"test"},
							},
						},
					},
				},
			}

			So(validatePluginCommands(project), ShouldResemble, ValidationErrors{})
		})
		Convey("no error should be thrown if a referenced plugin for a task does exist", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{
						Name: "compile",
						Commands: []model.PluginCommandConf{
							{
								Function: "",
								Command:  "archive.targz_pack",
								Params: map[string]interface{}{
									"target":     "tgz",
									"source_dir": "src",
									"include":    []string{":"},
								},
							},
						},
					},
				},
			}
			So(validatePluginCommands(project), ShouldResemble, ValidationErrors{})
		})
		Convey("no error should be thrown if a referenced plugin that exists contains unneeded parameters", func() {
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{
						Name: "compile",
						Commands: []model.PluginCommandConf{
							{
								Function: "",
								Command:  "archive.targz_pack",
								Params: map[string]interface{}{
									"target":     "tgz",
									"source_dir": "src",
									"include":    []string{":"},
									"extraneous": "G",
								},
							},
						},
					},
				},
			}
			So(validatePluginCommands(project), ShouldResemble, ValidationErrors{})
		})
		Convey("an error should be thrown if a referenced plugin contains invalid parameters", func() {
			params := map[string]interface{}{
				"aws_key":    "key",
				"aws_secret": "sec",
				"s3_copy_files": []interface{}{
					map[string]interface{}{
						"source": map[string]interface{}{
							"bucket": "long3nough",
							"path":   "fghij",
						},
						"destination": map[string]interface{}{
							"bucket": "..long-but-invalid",
							"path":   "fghij",
						},
					},
				},
			}
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{
						Name: "compile",
						Commands: []model.PluginCommandConf{
							{
								Function: "",
								Command:  "s3Copy.copy",
								Params:   params,
							},
						},
					},
				},
			}
			So(validatePluginCommands(project), ShouldNotResemble, ValidationErrors{})
			So(len(validatePluginCommands(project)), ShouldEqual, 1)
		})
		Convey("no error should be thrown if a referenced plugin that "+
			"exists contains params that appear invalid but are in expansions",
			func() {
				params := map[string]interface{}{
					"aws_key":    "key",
					"aws_secret": "sec",
					"s3_copy_files": []interface{}{
						map[string]interface{}{
							"source": map[string]interface{}{
								"bucket": "long3nough",
								"path":   "fghij",
							},
							"destination": map[string]interface{}{
								"bucket": "${..longButInvalid}",
								"path":   "fghij",
							},
						},
					},
				}
				project := &model.Project{
					Tasks: []model.ProjectTask{
						{
							Name: "compile",
							Commands: []model.PluginCommandConf{
								{
									Function: "",
									Command:  "s3Copy.copy",
									Params:   params,
								},
							},
						},
					},
				}
				So(validatePluginCommands(project), ShouldResemble, ValidationErrors{})
			})
		Convey("no error should be thrown if a referenced plugin contains all "+
			"the necessary and valid parameters", func() {
			params := map[string]interface{}{
				"aws_key":    "key",
				"aws_secret": "sec",
				"s3_copy_files": []interface{}{
					map[string]interface{}{
						"source": map[string]interface{}{
							"bucket": "abcde",
							"path":   "fghij",
						},
						"destination": map[string]interface{}{
							"bucket": "abcde",
							"path":   "fghij",
						},
					},
				},
			}
			project := &model.Project{
				Tasks: []model.ProjectTask{
					{
						Name: "compile",
						Commands: []model.PluginCommandConf{
							{
								Function: "",
								Command:  "s3Copy.copy",
								Params:   params,
							},
						},
					},
				},
			}
			So(validatePluginCommands(project), ShouldResemble, ValidationErrors{})
		})
	})
}

func TestCheckProjectWarnings(t *testing.T) {
	Convey("When validating a project's semantics", t, func() {
		Convey("if the project passes all of the validation funcs, no errors"+
			" should be returned", func() {
			distros := []distro.Distro{
				{Id: "test-distro-one"},
				{Id: "test-distro-two"},
			}

			for _, d := range distros {
				So(d.Insert(), ShouldBeNil)
			}

			projectRef := &model.ProjectRef{
				Id: "project_test",
			}

			_, project, err := model.FindLatestVersionWithValidProject(projectRef.Id)
			So(err, ShouldBeNil)
			So(CheckProjectWarnings(project), ShouldResemble, ValidationErrors{})
		})

		Reset(func() {
			So(db.Clear(distro.Collection), ShouldBeNil)
		})
	})
}

type validateProjectFieldsuite struct {
	suite.Suite
	project model.Project
}

func TestValidateProjectFieldsuite(t *testing.T) {
	suite.Run(t, new(validateProjectFieldsuite))
}

func (s *validateProjectFieldsuite) SetupTest() {
	s.project = model.Project{
		Enabled:     true,
		Identifier:  "identifier",
		Owner:       "owner",
		Repo:        "repo",
		Branch:      "branch",
		DisplayName: "test",
		BatchTime:   10,
	}
}

func (s *validateProjectFieldsuite) TestBatchTimeValueMustNonNegative() {
	s.project.BatchTime = -10
	validationError := validateProjectFields(&s.project)

	s.Len(validationError, 1)
	s.Contains(validationError[0].Message, "'batchtime' must be non-negative",
		"Project 'batchtime' must not be negative")
}

func (s *validateProjectFieldsuite) TestCommandTypes() {
	s.project.CommandType = "system"
	validationError := validateProjectFields(&s.project)
	s.Empty(validationError)

	s.project.CommandType = "test"
	validationError = validateProjectFields(&s.project)
	s.Empty(validationError)

	s.project.CommandType = "setup"
	validationError = validateProjectFields(&s.project)
	s.Empty(validationError)

	s.project.CommandType = ""
	validationError = validateProjectFields(&s.project)
	s.Empty(validationError)
}

func (s *validateProjectFieldsuite) TestFailOnInvalidCommandType() {
	s.project.CommandType = "random"
	validationError := validateProjectFields(&s.project)

	s.Len(validationError, 1)
	s.Contains(validationError[0].Message, "invalid command type: random",
		"Project 'CommandType' must be valid")
}

func (s *validateProjectFieldsuite) TestWarnOnLargeBatchTimeValue() {
	s.project.BatchTime = math.MaxInt32 + 1
	validationError := checkProjectFields(&s.project)

	s.Len(validationError, 1)
	s.Equal(validationError[0].Level, Warning,
		"Large batch time validation error should be a warning")
}

func TestValidateBVFields(t *testing.T) {
	Convey("When ensuring necessary buildvariant fields are set, ensure that", t, func() {
		Convey("an error is thrown if no build variants exist", func() {
			project := &model.Project{
				Identifier: "test",
			}
			So(validateBVFields(project),
				ShouldNotResemble, ValidationErrors{})
			So(len(validateBVFields(project)),
				ShouldEqual, 1)
		})
		Convey("buildvariants with none of the necessary fields set throw errors", func() {
			project := &model.Project{
				Identifier:    "test",
				BuildVariants: []model.BuildVariant{{}},
			}
			So(validateBVFields(project),
				ShouldNotResemble, ValidationErrors{})
			So(len(validateBVFields(project)),
				ShouldEqual, 2)
		})
		Convey("an error is thrown if the buildvariant does not have a "+
			"name field set", func() {
			project := &model.Project{
				Identifier: "projectId",
				BuildVariants: []model.BuildVariant{
					{
						RunOn: []string{"mongo"},
						Tasks: []model.BuildVariantTaskUnit{{Name: "db"}},
					},
				},
			}
			So(validateBVFields(project),
				ShouldNotResemble, ValidationErrors{})
			So(len(validateBVFields(project)),
				ShouldEqual, 1)
		})
		Convey("an error is thrown if the buildvariant does not have any tasks set", func() {
			project := &model.Project{
				Identifier: "projectId",
				BuildVariants: []model.BuildVariant{
					{
						Name:  "postal",
						RunOn: []string{"service"},
					},
				},
			}
			So(validateBVFields(project),
				ShouldNotResemble, ValidationErrors{})
			So(len(validateBVFields(project)),
				ShouldEqual, 1)
		})
		Convey("no error is thrown if the buildvariant has a run_on field set", func() {
			project := &model.Project{
				Identifier: "projectId",
				BuildVariants: []model.BuildVariant{
					{
						Name:  "import",
						RunOn: []string{"export"},
						Tasks: []model.BuildVariantTaskUnit{{Name: "db"}},
					},
				},
			}
			So(validateBVFields(project),
				ShouldResemble, ValidationErrors{})
		})
		Convey("an error should be thrown if the buildvariant has no "+
			"run_on field and at least one task has no distro field "+
			"specified", func() {
			project := &model.Project{
				Identifier: "projectId",
				BuildVariants: []model.BuildVariant{
					{
						Name:  "import",
						Tasks: []model.BuildVariantTaskUnit{{Name: "db"}},
					},
				},
			}
			So(validateBVFields(project),
				ShouldNotResemble, ValidationErrors{})
			So(len(validateBVFields(project)),
				ShouldEqual, 1)
		})
		Convey("no error should be thrown if the buildvariant does not "+
			"have a run_on field specified but the task definition has a "+
			"distro field specified", func() {
			project := &model.Project{
				Identifier: "projectId",
				BuildVariants: []model.BuildVariant{
					{
						Name: "import",
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name: "silhouettes",
							},
						},
					},
				},
				Tasks: []model.ProjectTask{
					{
						Name: "silhouettes",
						RunOn: []string{
							"echoes",
						},
					},
				},
			}
			So(validateBVFields(project),
				ShouldResemble, ValidationErrors{})
		})
		Convey("no error should be thrown if the buildvariant does not "+
			"have a run_on field specified but all tasks within it have a "+
			"distro field specified", func() {
			project := &model.Project{
				Identifier: "projectId",
				BuildVariants: []model.BuildVariant{
					{
						Name: "import",
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name: "silhouettes",
								RunOn: []string{
									"echoes",
								},
							},
						},
					},
				},
			}
			So(validateBVFields(project),
				ShouldResemble, ValidationErrors{})
		})
		Convey("blank distros should generate errors", func() {
			project := &model.Project{
				BuildVariants: model.BuildVariants{
					{
						Name:  "bv1",
						RunOn: []string{""},
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name:  "t1",
								RunOn: []string{""}},
						},
					},
				},
			}
			So(validateBVFields(project),
				ShouldResemble, ValidationErrors{
					{Level: Error, Message: "buildvariant 'bv1' must either specify run_on field or have every task specify run_on"},
				})
		})
	})
}
func TestTaskValidation(t *testing.T) {
	assert.New(t)
	simpleYml := `
  tasks:
  - name: task0
  - name: this task is too long
  buildvariants:
  - name: "bv"
    display_name: "bv_display"
    tasks:
    - name: task0
    - name: "this task is too long"
`
	var proj model.Project
	ctx := context.Background()
	_, err := model.LoadProjectInto(ctx, []byte(simpleYml), nil, "", &proj)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "spaces are not allowed")
}

func TestTaskGroupValidation(t *testing.T) {
	assert := assert.New(t)

	// check that yml with a task group with a duplicate task errors
	duplicateYml := `
  tasks:
  - name: example_task_1
  - name: example_task_2
  task_groups:
  - name: example_task_group
    tasks:
    - example_task_1
    - example_task_2
    - example_task_1
  buildvariants:
  - name: "bv"
    display_name: "bv_display"
    tasks:
    - name: example_task_group
  `
	var proj model.Project
	ctx := context.Background()
	pp, err := model.LoadProjectInto(ctx, []byte(duplicateYml), nil, "", &proj)
	assert.NotNil(proj)
	assert.NotNil(pp)
	assert.NoError(err)
	validationErrs := validateTaskGroups(&proj)
	assert.Len(validationErrs, 1)
	assert.Contains(validationErrs[0].Message, "example_task_1 is listed in task group example_task_group 2 times")

	proj = model.Project{
		Tasks: []model.ProjectTask{
			{Name: "task1"},
		},
		TaskGroups: []model.TaskGroup{
			{
				Name:  "tg1",
				Tasks: []string{"task1"},
			},
			{
				Name:  "tg1",
				Tasks: []string{"task1"},
			},
		},
	}
	assert.Len(checkTaskGroups(&proj), 3)

	// check that yml with a task group named the same as a task errors
	duplicateTaskYml := `
  tasks:
  - name: foo
  - name: example_task_2
  task_groups:
  - name: foo
    tasks:
    - example_task_2
  buildvariants:
  - name: "bv"
    display_name: "bv_display"
    tasks:
    - name: foo
  `
	pp, err = model.LoadProjectInto(ctx, []byte(duplicateTaskYml), nil, "", &proj)
	assert.NotNil(proj)
	assert.NotNil(pp)
	assert.NoError(err)
	validationErrs = validateTaskGroups(&proj)
	assert.Len(validationErrs, 1)
	assert.Contains(validationErrs[0].Message, "foo is used as a name for both a task and task group")

	largeMaxHostYml := `
tasks:
- name: example_task_1
- name: example_task_2
- name: example_task_3
task_groups:
- name: example_task_group
  max_hosts: 4
  tasks:
  - example_task_1
  - example_task_2
  - example_task_3
buildvariants:
- name: "bv"
  display_name: "bv_display"
  tasks:
  - name: example_task_group
`
	pp, err = model.LoadProjectInto(ctx, []byte(largeMaxHostYml), nil, "", &proj)
	assert.NotNil(proj)
	assert.NotNil(pp)
	assert.NoError(err)
	validationErrs = checkTaskGroups(&proj)
	assert.Len(validationErrs, 1)
	assert.Contains(validationErrs[0].Message, "task group example_task_group has max number of hosts 4 greater than the number of tasks 3")
	assert.Equal(validationErrs[0].Level, Warning)
}

func TestTaskGroupTeardownValidation(t *testing.T) {
	baseYml := `
tasks:
- name: example_task_1
- name: example_task_2

buildvariants:
- name: "bv"
  display_name: "bv_display"
  tasks:
  - name: example_task_group
task_groups:
- name: example_task_group
  setup_group:
  - command: shell.exec
    params:
      script: "echo setup_group"
  tasks:
  - example_task_1
  - example_task_2
`

	var proj model.Project
	ctx := context.Background()
	// verify that attach commands can't be used in teardown group
	for _, commandName := range evergreen.AttachCommands {
		attachCommand := fmt.Sprintf(`
  teardown_group:
  - command: %s
`, commandName)
		attachTeardownYml := fmt.Sprintf("%s\n%s", baseYml, attachCommand)
		pp, err := model.LoadProjectInto(ctx, []byte(attachTeardownYml), nil, "", &proj)
		assert.NotNil(t, proj)
		assert.NotNil(t, pp)
		assert.NoError(t, err)
		validationErrs := validateTaskGroups(&proj)
		assert.Len(t, validationErrs, 1)
		assert.Contains(t, validationErrs[0].Message, fmt.Sprintf("%s cannot be used in the group teardown stage", commandName))
	}

}

func TestTaskNotInTaskGroupDependsOnTaskInTaskGroup(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	require.NoError(db.Clear(distro.Collection))
	d := distro.Distro{Id: "example_distro"}
	require.NoError(d.Insert())
	exampleYml := `
exec_timeout_secs: 100
tasks:
- name: not_in_a_task_group
  exec_timeout_secs: 100
  commands:
  - command: shell.exec
    params:
      script: echo test
  depends_on:
  - name: task_in_a_task_group_1
- name: task_in_a_task_group_1
  exec_timeout_secs: 100
  commands:
  - command: shell.exec
    params:
      script: echo test
- name: task_in_a_task_group_2
  exec_timeout_secs: 100
  commands:
  - command: shell.exec
    params:
      script: echo test
task_groups:
- name: example_task_group
  max_hosts: 1
  tasks:
  - task_in_a_task_group_1
  - task_in_a_task_group_2
buildvariants:
- name: "bv"
  display_name: "bv_display"
  run_on: "example_distro"
  tasks:
  - name: not_in_a_task_group
  - name: example_task_group
`
	proj := model.Project{}
	ctx := context.Background()
	pp, err := model.LoadProjectInto(ctx, []byte(exampleYml), nil, "example_project", &proj)
	assert.NotNil(proj)
	assert.NotNil(pp)
	assert.NoError(err)
	assert.Len(proj.TaskGroups, 1)
	tg := proj.TaskGroups[0]
	assert.Equal("example_task_group", tg.Name)
	assert.Len(tg.Tasks, 2)
	assert.Equal("not_in_a_task_group", proj.Tasks[0].Name)
	assert.Equal("task_in_a_task_group_1", proj.Tasks[0].DependsOn[0].Name)
	errors := CheckProjectErrors(&proj, false)
	assert.Len(errors, 0)
	warnings := CheckProjectWarnings(&proj)
	assert.Len(warnings, 0)
}

func TestDisplayTaskExecutionTasksNameValidation(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	require.NoError(db.Clear(distro.Collection))
	d := distro.Distro{Id: "example_distro"}
	require.NoError(d.Insert())
	exampleYml := `
tasks:
- name: one
  exec_timeout_secs: 100
  commands:
  - command: shell.exec
    params:
      script: |
        echo "test"
- name: two
  exec_timeout_secs: 100
  commands:
  - command: shell.exec
    params:
      script: echo "test"
- name: display_three
  exec_timeout_secs: 100 
  commands:
  - command: shell.exec
    params:
      script: echo "test"
buildvariants:
- name: "bv"
  display_name: "bv_display"
  run_on: "example_distro"
  tasks:
  - name: one
  - name: two
  display_tasks:
  - name: display_ordinals
    execution_tasks:
    - one
    - two
`
	proj := model.Project{}
	ctx := context.Background()
	pp, err := model.LoadProjectInto(ctx, []byte(exampleYml), nil, "example_project", &proj)
	assert.NotNil(proj)
	assert.NotNil(pp)
	assert.NoError(err)

	proj.BuildVariants[0].DisplayTasks[0].ExecTasks = append(proj.BuildVariants[0].DisplayTasks[0].ExecTasks,
		"display_three")

	errors := CheckProjectErrors(&proj, false)
	assert.Len(errors, 1)
	assert.Equal(errors[0].Level, Error)
	assert.Equal("execution task 'display_three' has prefix 'display_' which is invalid",
		errors[0].Message)
	warnings := CheckProjectWarnings(&proj)
	assert.Len(warnings, 0)
}

func TestValidateCreateHosts(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	// passing case
	yml := `
  tasks:
  - name: t_1
    commands:
    - command: host.create
  buildvariants:
  - name: "bv"
    display_name: "bv_display"
    tasks:
    - name: t_1
  `
	var p model.Project
	ctx := context.Background()
	pp, err := model.LoadProjectInto(ctx, []byte(yml), nil, "id", &p)
	require.NoError(err)
	require.NotNil(pp)
	errs := validateHostCreates(&p)
	assert.Len(errs, 0)

	// error: times called per task
	yml = `
  tasks:
  - name: t_1
    commands:
    - command: host.create
    - command: host.create
    - command: host.create
    - command: host.create
  buildvariants:
  - name: "bv"
    display_name: "bv_display"
    tasks:
    - name: t_1
  `
	pp, err = model.LoadProjectInto(ctx, []byte(yml), nil, "id", &p)
	require.NoError(err)
	require.NotNil(pp)
	errs = validateHostCreates(&p)
	assert.Len(errs, 1)
}

func TestValidateParameters(t *testing.T) {
	p := &model.Project{
		Parameters: []model.ParameterInfo{
			{
				Parameter: patch.Parameter{
					Key:   "iter=count",
					Value: "",
				},
			},
		},
	}

	assert.Len(t, validateParameters(p), 1)
	p.Parameters[0].Parameter.Key = ""
	assert.Len(t, validateParameters(p), 1)
	p.Parameters[0].Parameter.Key = "iter_count"
	assert.Len(t, validateParameters(p), 0)
	p.Parameters[0].Description = "not validated"
	p.Parameters[0].Value = "also not"
	assert.Len(t, validateParameters(p), 0)
}

func TestDuplicateTaskInBV(t *testing.T) {
	assert := assert.New(t)

	// a bv with the same task in a task group and by itself should error
	yml := `
  tasks:
  - name: t1
  task_groups:
  - name: tg1
    tasks:
    - t1
  buildvariants:
  - name: "bv"
    display_name: "bv_display"
    tasks:
    - tg1
    - t1
  `
	var p model.Project
	ctx := context.Background()
	pp, err := model.LoadProjectInto(ctx, []byte(yml), nil, "", &p)
	assert.NoError(err)
	assert.NotNil(pp)
	errs := validateDuplicateBVTasks(&p)
	assert.Len(errs, 1)
	assert.Contains(errs[0].Message, "task 't1' in 'bv' is listed more than once")

	// same as above but reversed in order
	yml = `
  tasks:
  - name: t1
  task_groups:
  - name: tg1
    tasks:
    - t1
  buildvariants:
  - name: "bv"
    display_name: "bv_display"
    tasks:
    - t1
    - tg1
  `
	pp, err = model.LoadProjectInto(ctx, []byte(yml), nil, "", &p)
	assert.NoError(err)
	assert.NotNil(pp)
	errs = validateDuplicateBVTasks(&p)
	assert.Len(errs, 1)
	assert.Contains(errs[0].Message, "task 't1' in 'bv' is listed more than once")

	// a bv with 2 task groups with the same task should error
	yml = `
  tasks:
  - name: t1
  task_groups:
  - name: tg1
    tasks:
    - t1
  - name: tg2
    tasks:
    - t1
  buildvariants:
  - name: "bv"
    display_name: "bv_display"
    tasks:
    - tg1
    - tg2
  `
	pp, err = model.LoadProjectInto(ctx, []byte(yml), nil, "", &p)
	assert.NoError(err)
	assert.NotNil(pp)
	errs = validateDuplicateBVTasks(&p)
	assert.Len(errs, 1)
	assert.Contains(errs[0].Message, "task 't1' in 'bv' is listed more than once")
}

func TestLoggerConfig(t *testing.T) {
	assert := assert.New(t)
	yml := `
loggers:
  agent:
  - type: splunk
    splunk_token: idk
  task:
  - type: somethingElse
tasks:
- name: task_1
  commands:
  - command: myCommand
    display_name: foo
    loggers:
      system:
      - type: commandLogger
`
	project := &model.Project{}
	ctx := context.Background()
	pp, err := model.LoadProjectInto(ctx, []byte(yml), nil, "", project)
	assert.NoError(err)
	assert.NotNil(pp)
	errs := checkTasks(project)
	assert.Contains(errs.String(), "error in project-level logger config: invalid agent logger config: Splunk logger requires a server URL")
	assert.Contains(errs.String(), "invalid task logger config: 'somethingElse' is not a valid log sender")
	assert.Contains(errs.String(), "error in logger config for command foo in task task_1: invalid system logger config: 'commandLogger' is not a valid log sender")

	// no loggers specified should not error
	yml = `
repo: asdf
tasks:
- name: task_1
  commands:
  - command: myCommand
  display_name: foo
    `

	project = &model.Project{}
	pp, err = model.LoadProjectInto(ctx, []byte(yml), nil, "", project)
	assert.NoError(err)
	assert.NotNil(pp)
	errs = checkLoggerConfig(&project.Tasks[0])
	assert.Len(errs, 0)
}

func TestCheckProjectConfigurationIsValid(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	require.NoError(db.Clear(distro.Collection))
	d := distro.Distro{Id: "example_distro"}
	require.NoError(d.Insert())
	exampleYml := `
tasks:
- name: one
  commands:
  - command: shell.exec
    params: 
      script: | 
        echo test
- name: two
  commands:
  - command: shell.exec
    params:
      script: |
        echo test
buildvariants:
- name: "bv-1"
  display_name: "bv_display"
  run_on: "example_distro"
  tasks:
  - name: one
  - name: two
- name: "bv-2"
  display_name: "bv_display"
  run_on: "example_distro"
  tasks:
  - name: one
  - name: two
`
	proj := model.Project{}
	ctx := context.Background()
	pp, err := model.LoadProjectInto(ctx, []byte(exampleYml), nil, "example_project", &proj)
	require.NoError(err)
	assert.NotEmpty(proj)
	assert.NotNil(pp)
	errs := CheckProjectErrors(&proj, false)
	assert.Len(errs, 0, "no errors were found")
	errs = CheckProjectWarnings(&proj)
	assert.Len(errs, 2, "two warnings were found")
	assert.NoError(CheckProjectConfigurationIsValid(&proj, &model.ProjectRef{}), "no errors are reported because they are warnings")

	exampleYml = `
tasks:
  - name: taskA
    commands:
    - command: s3.push
    - command: s3.push
buildvariants:
  - name: bvA
    display_name: "bvA_display"
    run_on: example_distro
    tasks:
      - name: taskA
`
	pp, err = model.LoadProjectInto(ctx, []byte(exampleYml), nil, "example_project", &proj)
	require.NoError(err)
	assert.NotNil(pp)
	assert.NotEmpty(proj)
	assert.Error(CheckProjectConfigurationIsValid(&proj, &model.ProjectRef{}))
}

func TestGetDistrosForProject(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)
	require.NoError(db.Clear(distro.Collection))
	d := distro.Distro{
		Id:            "distro1",
		Aliases:       []string{"distro1-alias", "distro1and2-alias"},
		ValidProjects: []string{"project1", "project2"},
	}
	require.NoError(d.Insert())
	d = distro.Distro{
		Id:      "distro2",
		Aliases: []string{"distro2-alias", "distro1and2-alias"},
	}
	require.NoError(d.Insert())
	d = distro.Distro{
		Id:            "distro3",
		ValidProjects: []string{"project5"},
	}
	require.NoError(d.Insert())

	ids, aliases, err := getDistros()
	require.NoError(err)
	require.Len(ids, 3)
	require.Len(aliases, 3)
	assert.Contains(aliases, "distro1and2-alias")
	assert.Contains(aliases, "distro1-alias")
	assert.Contains(aliases, "distro2-alias")
	ids, aliases, err = getDistrosForProject("project1")
	require.NoError(err)
	require.Len(ids, 2)
	assert.Contains(ids, "distro1")
	assert.Contains(aliases, "distro1and2-alias")
	assert.Contains(aliases, "distro1-alias")
	ids, aliases, err = getDistrosForProject("project3")
	require.NoError(err)
	require.Len(ids, 1)
	assert.Contains(ids, "distro2")
	assert.Contains(aliases, "distro2-alias")
	assert.Contains(aliases, "distro1and2-alias")
}

func TestValidateTaskSyncCommands(t *testing.T) {
	t.Run("TaskWithNoS3PushCallsPasses", func(t *testing.T) {
		p := &model.Project{
			Tasks: []model.ProjectTask{
				{
					Name:     t.Name(),
					Commands: []model.PluginCommandConf{},
				},
			},
		}
		assert.Empty(t, validateTaskSyncCommands(p, false))
	})
	t.Run("TaskWithMultipleS3PushCallsFails", func(t *testing.T) {
		p := &model.Project{
			Tasks: []model.ProjectTask{
				{
					Name: t.Name(),
					Commands: []model.PluginCommandConf{
						{
							Command: evergreen.S3PushCommandName,
						},
						{
							Command: evergreen.S3PushCommandName,
						},
					},
				},
			},
			BuildVariants: []model.BuildVariant{
				{
					Name: "build_variant",
					Tasks: []model.BuildVariantTaskUnit{
						{
							Name: t.Name(),
						},
					},
				},
			},
		}
		assert.NotEmpty(t, validateTaskSyncCommands(p, false))
	})
}

func TestValidateVersionControl(t *testing.T) {
	ref := &model.ProjectRef{
		Identifier:            "proj",
		VersionControlEnabled: utility.FalsePtr(),
	}
	projectConfig := model.ProjectConfig{
		Id: "proj",
		ProjectConfigFields: model.ProjectConfigFields{
			BuildBaronSettings: &evergreen.BuildBaronSettings{
				TicketCreateProject:  "ABC",
				TicketSearchProjects: []string{"EVG"},
			},
		},
	}
	isConfigDefined := &projectConfig != nil
	verrs := validateVersionControl(&model.Project{}, ref, isConfigDefined)
	assert.Equal(t, "version control is disabled for project 'proj'; the currently defined project config fields will not be picked up", verrs[0].Message)

	ref.VersionControlEnabled = utility.TruePtr()
	verrs = validateVersionControl(&model.Project{}, ref, false)
	assert.Equal(t, "version control is enabled for project 'proj' but no project config fields have been set.", verrs[0].Message)

}

func TestValidateContainers(t *testing.T) {
	require.NoError(t, db.Clear(model.ProjectRefCollection))
	ref := &model.ProjectRef{
		Identifier: "proj",
		ContainerSizes: map[string]model.ContainerResources{
			"s1": model.ContainerResources{
				MemoryMB: 100,
				CPU:      1,
			},
		},
		ContainerCredentials: map[string]model.ContainerCredential{
			"c1": model.ContainerCredential{
				Username: "foo",
				Password: "bar",
			},
		},
	}
	require.NoError(t, ref.Insert())
	p := &model.Project{
		Identifier: "proj",
		Containers: []model.Container{
			{
				Name:       "c1",
				Image:      "demo/image:latest",
				WorkingDir: "/root",
				Size:       "s1",
				Credential: "c1",
			},
		},
	}
	verrs := validateContainers(p, ref, false)
	assert.Len(t, verrs, 0)
	p.Containers[0].Name = ""
	verrs = validateContainers(p, ref, false)
	require.Len(t, verrs, 1)
	assert.Contains(t, verrs[0].Message, "name must be defined")
	p.Containers[0].Name = "c1"
	p.Containers[0].Image = ""
	verrs = validateContainers(p, ref, false)
	require.Len(t, verrs, 1)
	assert.Contains(t, verrs[0].Message, "image must be defined")
	p.Containers[0].Image = "demo:image"
	p.Containers[0].Resources = &model.ContainerResources{
		MemoryMB: 100,
		CPU:      1,
	}
	verrs = validateContainers(p, ref, false)
	require.Len(t, verrs, 1)
	assert.Contains(t, verrs[0].Message, "size and resources cannot both be defined")
	p.Containers[0].Size = ""
	p.Containers[0].Resources = nil
	verrs = validateContainers(p, ref, false)
	require.Len(t, verrs, 1)
	assert.Contains(t, verrs[0].Message, "either size or resources must be defined")
	p.Containers[0].Size = "s2"
	verrs = validateContainers(p, ref, false)
	require.Len(t, verrs, 1)
	assert.Contains(t, verrs[0].Message, "size 's2' is not defined anywhere")
	p.Containers[0].Credential = "c2"
	verrs = validateContainers(p, ref, false)
	require.Len(t, verrs, 1)
	assert.Contains(t, verrs[0].Message, "credential 'c2' is not defined anywhere")
	p.Containers[0].System = model.ContainerSystem{
		OperatingSystem: "oops",
		CPUArchitecture: "oops",
	}
	verrs = validateContainers(p, ref, false)
	require.Len(t, verrs, 1)
	assert.Contains(t, verrs[0].Message, "unrecognized container OS 'oops'")
	assert.Contains(t, verrs[0].Message, "unrecognized CPU architecture 'oops'")
	p.Containers[0].System = model.ContainerSystem{
		OperatingSystem: evergreen.LinuxOS,
		CPUArchitecture: evergreen.ArchARM64,
	}
	p.Containers[0].Resources = &model.ContainerResources{
		MemoryMB: 0,
		CPU:      -1,
	}
	verrs = validateContainers(p, ref, false)
	require.Len(t, verrs, 1)
	assert.Contains(t, verrs[0].Message, "container resource CPU must be a positive integer")
	assert.Contains(t, verrs[0].Message, "container resource memory MB must be a positive integer")
}

func TestValidateTaskSyncSettings(t *testing.T) {
	for testName, testParams := range map[string]struct {
		tasks                    []model.ProjectTask
		taskSyncEnabledForConfig bool
		expectError              bool
	}{
		"NoTaskSyncPasses": {
			expectError: false,
		},
		"ConfigWithTaskSyncWhenEnabledPasses": {
			taskSyncEnabledForConfig: true,
			tasks: []model.ProjectTask{
				{
					Commands: []model.PluginCommandConf{
						{
							Command: evergreen.S3PushCommandName,
						},
					},
				},
			},
			expectError: false,
		},
		"ConfigWithS3PushWhenDisabledFails": {
			tasks: []model.ProjectTask{
				{
					Commands: []model.PluginCommandConf{
						{
							Command: evergreen.S3PushCommandName,
						},
					},
				},
			},
			expectError: true,
		},
		"ConfigWithS3PullWhenDisabledFails": {
			tasks: []model.ProjectTask{
				{
					Commands: []model.PluginCommandConf{
						{
							Command: evergreen.S3PullCommandName,
						},
					},
				},
			},
			expectError: true,
		},
		"ConfigWithoutTaskSyncWhenEnabledPasses": {
			taskSyncEnabledForConfig: true,
			expectError:              false,
		},
	} {
		t.Run(testName, func(t *testing.T) {
			ref := &model.ProjectRef{
				TaskSync: model.TaskSyncOptions{
					ConfigEnabled: &testParams.taskSyncEnabledForConfig,
				},
			}
			p := &model.Project{Tasks: testParams.tasks}
			errs := validateTaskSyncSettings(p, ref, false)
			if testParams.expectError {
				assert.NotEmpty(t, errs)
			} else {
				assert.Empty(t, errs)
			}
		})
	}
	ref := &model.ProjectRef{}
	p := &model.Project{
		Tasks: []model.ProjectTask{
			{
				Commands: []model.PluginCommandConf{
					{
						Command: evergreen.S3PushCommandName,
					},
				},
			},
		},
	}
	assert.NotEmpty(t, validateTaskSyncSettings(p, ref, false))

	ref.TaskSync.ConfigEnabled = utility.TruePtr()
	assert.Empty(t, validateTaskSyncSettings(p, ref, false))

	p.Tasks = []model.ProjectTask{}
	assert.Empty(t, validateTaskSyncSettings(p, ref, false))
}

func TestTVToTaskUnit(t *testing.T) {
	for testName, testCase := range map[string]struct {
		expectedTVToTaskUnit map[model.TVPair]model.BuildVariantTaskUnit
		project              model.Project
	}{
		"MapsTasksAndPopulates": {
			expectedTVToTaskUnit: map[model.TVPair]model.BuildVariantTaskUnit{
				{TaskName: "setup", Variant: "rhel"}: {
					Name:            "setup",
					Variant:         "rhel",
					Priority:        20,
					ExecTimeoutSecs: 20,
				}, {TaskName: "compile", Variant: "ubuntu"}: {
					Name:             "compile",
					Variant:          "ubuntu",
					ExecTimeoutSecs:  10,
					CommitQueueMerge: true,
					DependsOn: []model.TaskUnitDependency{
						{
							Name:    "setup",
							Variant: "rhel",
						},
					},
				}, {TaskName: "compile", Variant: "suse"}: {
					Name:            "compile",
					Variant:         "suse",
					ExecTimeoutSecs: 10,
					DependsOn: []model.TaskUnitDependency{
						{
							Name:    "setup",
							Variant: "rhel",
						},
					},
				},
			},
			project: model.Project{
				Tasks: []model.ProjectTask{
					{
						Name:            "setup",
						Priority:        10,
						ExecTimeoutSecs: 10,
					}, {
						Name:            "compile",
						ExecTimeoutSecs: 10,
						DependsOn: []model.TaskUnitDependency{
							{
								Name:    "setup",
								Variant: "rhel",
							},
						},
					},
				},
				BuildVariants: []model.BuildVariant{
					{
						Name: "rhel",
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name:            "setup",
								Priority:        20,
								ExecTimeoutSecs: 20,
							},
						},
					}, {
						Name: "ubuntu",
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name:             "compile",
								CommitQueueMerge: true,
								ExecTimeoutSecs:  10,
								DependsOn: []model.TaskUnitDependency{
									{
										Name:    "setup",
										Variant: "rhel",
									},
								},
							},
						},
					}, {
						Name: "suse",
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name:            "compile",
								ExecTimeoutSecs: 10,
								DependsOn: []model.TaskUnitDependency{
									{
										Name:    "setup",
										Variant: "rhel",
									},
								},
							},
						},
					},
				},
			},
		},
		"MapsTaskGroupTasksAndPopulates": {
			expectedTVToTaskUnit: map[model.TVPair]model.BuildVariantTaskUnit{
				{TaskName: "setup", Variant: "rhel"}: {
					Name:            "setup",
					Variant:         "rhel",
					Priority:        20,
					ExecTimeoutSecs: 20,
				}, {TaskName: "compile", Variant: "ubuntu"}: {
					Name:             "compile",
					Variant:          "ubuntu",
					IsGroup:          true,
					GroupName:        "compile_group",
					ExecTimeoutSecs:  10,
					CommitQueueMerge: true,
					DependsOn: []model.TaskUnitDependency{
						{
							Name:    "setup",
							Variant: "rhel",
						},
					},
				}, {TaskName: "compile", Variant: "suse"}: {
					Name:            "compile",
					Variant:         "suse",
					IsGroup:         true,
					GroupName:       "compile_group",
					ExecTimeoutSecs: 10,
					DependsOn: []model.TaskUnitDependency{
						{
							Name:    "setup",
							Variant: "rhel",
						},
					},
				},
			},
			project: model.Project{
				TaskGroups: []model.TaskGroup{
					{
						Name:  "compile_group",
						Tasks: []string{"compile"},
					},
				},
				Tasks: []model.ProjectTask{
					{
						Name:            "setup",
						Priority:        10,
						ExecTimeoutSecs: 10,
					}, {
						Name:            "compile",
						ExecTimeoutSecs: 10,
						DependsOn: []model.TaskUnitDependency{
							{
								Name:    "setup",
								Variant: "rhel",
							},
						},
					},
				},
				BuildVariants: []model.BuildVariant{
					{
						Name: "rhel",
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name:            "setup",
								Priority:        20,
								ExecTimeoutSecs: 20,
							},
						},
					}, {
						Name: "ubuntu",
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name:             "compile_group",
								CommitQueueMerge: true,
							},
						},
					}, {
						Name: "suse",
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name: "compile_group",
							},
						},
					},
				},
			},
		},
	} {
		t.Run(testName, func(t *testing.T) {
			tvToTaskUnit := tvToTaskUnit(&testCase.project)
			assert.Len(t, tvToTaskUnit, len(testCase.expectedTVToTaskUnit))
			for expectedTV := range testCase.expectedTVToTaskUnit {
				assert.Contains(t, tvToTaskUnit, expectedTV)
				taskUnit := tvToTaskUnit[expectedTV]
				expectedTaskUnit := testCase.expectedTVToTaskUnit[expectedTV]
				assert.Equal(t, expectedTaskUnit.Name, taskUnit.Name)
				assert.Equal(t, expectedTaskUnit.IsGroup, taskUnit.IsGroup, fmt.Sprintf("%s/%s", expectedTaskUnit.Variant, expectedTaskUnit.Name))
				assert.Equal(t, expectedTaskUnit.GroupName, taskUnit.GroupName, fmt.Sprintf("%s/%s", expectedTaskUnit.Variant, expectedTaskUnit.Name))
				assert.Equal(t, expectedTaskUnit.Patchable, taskUnit.Patchable, expectedTaskUnit.Name)
				assert.Equal(t, expectedTaskUnit.PatchOnly, taskUnit.PatchOnly)
				assert.Equal(t, expectedTaskUnit.Priority, taskUnit.Priority)
				missingActual, missingExpected := utility.StringSliceSymmetricDifference(expectedTaskUnit.RunOn, taskUnit.RunOn)
				assert.Empty(t, missingActual)
				assert.Empty(t, missingExpected)
				assert.Len(t, taskUnit.DependsOn, len(expectedTaskUnit.DependsOn))
				for _, dep := range expectedTaskUnit.DependsOn {
					assert.Contains(t, taskUnit.DependsOn, dep)
				}
				assert.Equal(t, expectedTaskUnit.ExecTimeoutSecs, taskUnit.ExecTimeoutSecs)
				assert.Equal(t, expectedTaskUnit.Stepback, taskUnit.Stepback)
				assert.Equal(t, expectedTaskUnit.CommitQueueMerge, taskUnit.CommitQueueMerge, fmt.Sprintf("%s/%s", expectedTaskUnit.Variant, expectedTaskUnit.Name))
				assert.Equal(t, expectedTaskUnit.Variant, taskUnit.Variant)
			}
		})
	}
}

func TestValidateTVDependsOnTV(t *testing.T) {
	for testName, testCase := range map[string]struct {
		dependedOnTask model.TVPair
		dependentTask  model.TVPair
		statuses       []string
		buildVariants  []model.BuildVariant
		expectError    bool
	}{
		"FindsDependency": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "B", Variant: "ubuntu"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{
							Name: "A",
							DependsOn: []model.TaskUnitDependency{
								{Name: "B"},
							},
						},
						{Name: "B"},
					},
				},
			},
			expectError: false,
		},
		"FindsDependencyWithoutExplicitBV": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "B", Variant: "ubuntu"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{Name: "A", DependsOn: []model.TaskUnitDependency{{Name: "B"}}},
						{Name: "B"},
					},
				},
			},
			expectError: false,
		},
		"FindsDependencyTransitively": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "C", Variant: "rhel"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{Name: "A", DependsOn: []model.TaskUnitDependency{{Name: "B"}}},
						{Name: "B", DependsOn: []model.TaskUnitDependency{{Name: "C", Variant: "rhel"}}},
					},
				},
				{
					Name: "rhel",
					Tasks: []model.BuildVariantTaskUnit{
						{Name: "C"},
					},
				},
			},
			expectError: false,
		},
		"FailsForNoDependency": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "B", Variant: "ubuntu"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{Name: "A"},
					},
				},
			},
			expectError: true,
		},
		"FailsIfDependencySkipsPatches": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "B", Variant: "ubuntu"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{Name: "A", DependsOn: []model.TaskUnitDependency{{Name: "B", Variant: "ubuntu"}}},
						{Name: "B", Patchable: utility.FalsePtr()},
					},
				},
			},
			expectError: true,
		},
		"FailsIfIntermediateDependencySkipsPatches": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "C", Variant: "rhel"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{Name: "A", DependsOn: []model.TaskUnitDependency{{Name: "B"}}},
						{
							Name:      "B",
							Variant:   "ubuntu",
							Patchable: utility.FalsePtr(),
							DependsOn: []model.TaskUnitDependency{
								{Name: "C", Variant: "rhel"},
							},
						},
					},
				},
				{
					Name: "rhel",
					Tasks: []model.BuildVariantTaskUnit{
						{Name: "C"},
					},
				},
			},
			expectError: true,
		},
		"FailsIfDependencySkipsNonPatches": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "B", Variant: "ubuntu"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{Name: "A", DependsOn: []model.TaskUnitDependency{{Name: "B", Variant: "ubuntu"}}},
						{Name: "B", Patchable: utility.FalsePtr()},
					},
				},
			},
			expectError: true,
		},
		"FailsIfIntermediateDependencySkipsNonPatches": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "C", Variant: "rhel"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{Name: "A", DependsOn: []model.TaskUnitDependency{{Name: "B"}}},
						{
							Name:      "B",
							PatchOnly: utility.TruePtr(),
							DependsOn: []model.TaskUnitDependency{
								{Name: "C", Variant: "rhel"},
							},
						},
					},
				},
				{
					Name: "rhel",
					Tasks: []model.BuildVariantTaskUnit{
						{Name: "C"},
					},
				},
			},
			expectError: true,
		},
		"FailsIfDependencyIsPatchOptional": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "B", Variant: "ubuntu"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{
							Name: "A",
							DependsOn: []model.TaskUnitDependency{
								{
									Name:          "B",
									Variant:       "ubuntu",
									PatchOptional: true,
								},
							},
						},
						{Name: "B"},
					},
				},
			},
			expectError: true,
		},
		"FailsIfIntermediateDependencyIsPatchOptional": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "C", Variant: "rhel"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{
							Name: "A",
							DependsOn: []model.TaskUnitDependency{
								{Name: "B", Variant: "ubuntu"},
							},
						},
						{
							Name: "B",
							DependsOn: []model.TaskUnitDependency{
								{Name: "C", Variant: "rhel", PatchOptional: true},
							},
						},
					},
				},
				{
					Name: "rhel",
					Tasks: []model.BuildVariantTaskUnit{
						{Name: "C"},
					},
				},
			},
			expectError: true,
		},
		"OnlyLastDependencyRequiresSuccessStatus": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "C", Variant: "rhel"},
			statuses: []string{
				evergreen.TaskSucceeded,
				"",
			},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{
							Name: "A",
							DependsOn: []model.TaskUnitDependency{
								{Name: "B", Status: evergreen.TaskFailed},
							},
						},
						{
							Name: "B",
							DependsOn: []model.TaskUnitDependency{
								{Name: "C", Variant: "rhel"},
							},
						},
					},
				},
				{
					Name: "rhel",
					Tasks: []model.BuildVariantTaskUnit{
						{Name: "C"},
					},
				},
			},
			expectError: false,
		},
		"FailsIfDependencyDoesNotRequireSuccessStatus": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "B", Variant: "ubuntu"},
			statuses: []string{
				evergreen.TaskSucceeded,
				"",
			},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{
							Name: "A",
							DependsOn: []model.TaskUnitDependency{
								{
									Name:    "B",
									Variant: "ubuntu",
									Status:  evergreen.TaskFailed,
								},
							},
						},
						{Name: "B"},
					},
				},
			},
			expectError: true,
		},
		"FailsIfLastDependencyDoesNotRequireSuccessStatus": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "C", Variant: "rhel"},
			statuses: []string{
				evergreen.TaskSucceeded,
				"",
			},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{
							Name: "A",
							DependsOn: []model.TaskUnitDependency{
								{Name: "B"},
							},
						},
						{
							Name: "B",
							DependsOn: []model.TaskUnitDependency{
								{Name: "C", Variant: "rhel", Status: evergreen.TaskFailed},
							},
						},
					},
				},
				{
					Name: "rhel",
					Tasks: []model.BuildVariantTaskUnit{
						{Name: "C"},
					},
				},
			},
			expectError: true,
		},
		"DependencyCanSkipPatchesIfSourceSkipsPatches": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "B", Variant: "ubuntu"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{
							Name:      "A",
							Patchable: utility.FalsePtr(),
							DependsOn: []model.TaskUnitDependency{
								{Name: "B", Variant: "ubuntu"},
							},
						},
						{
							Name:      "B",
							Patchable: utility.FalsePtr(),
						},
					},
				},
			},
			expectError: false,
		},
		"IntermediateDependencyCanSkipPatchesIfSourceSkipsPatches": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "C", Variant: "rhel"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{
							Name:      "A",
							Patchable: utility.FalsePtr(),
							DependsOn: []model.TaskUnitDependency{
								{Name: "B"},
							},
						},
						{
							Name:      "B",
							Patchable: utility.FalsePtr(),
							DependsOn: []model.TaskUnitDependency{
								{Name: "C", Variant: "rhel", Status: evergreen.TaskFailed},
							},
						},
					},
				},
				{
					Name: "rhel",
					Tasks: []model.BuildVariantTaskUnit{
						{Name: "C"},
					},
				},
			},
			expectError: false,
		},
		"DependencyCanSkipNonPatchesIfSourceSkipsNonPatches": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "B", Variant: "ubuntu"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{
							Name:      "A",
							PatchOnly: utility.TruePtr(),
							DependsOn: []model.TaskUnitDependency{
								{Name: "B", Variant: "ubuntu"},
							},
						},
						{
							Name:      "B",
							PatchOnly: utility.TruePtr(),
						},
					},
				},
			},
			expectError: false,
		},
		"IntermediateDependencyCanSkipNonPatchesIfSourceSkipsNonPatches": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "C", Variant: "rhel"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{
							Name:      "A",
							PatchOnly: utility.TruePtr(),
							DependsOn: []model.TaskUnitDependency{
								{Name: "B"},
							},
						},
						{
							Name:      "B",
							PatchOnly: utility.TruePtr(),
							DependsOn: []model.TaskUnitDependency{
								{Name: "C", Variant: "rhel"},
							},
						},
					},
				},
				{
					Name: "rhel",
					Tasks: []model.BuildVariantTaskUnit{
						{Name: "C"},
					},
				},
			},
			expectError: false,
		},
		"DependencySkipsGitTagsIfSourceRequiresPatches": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "B", Variant: "ubuntu"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{
							Name:      "A",
							PatchOnly: utility.TruePtr(),
							DependsOn: []model.TaskUnitDependency{
								{Name: "B", Variant: "ubuntu"},
							},
						},
						{
							Name:       "B",
							GitTagOnly: utility.TruePtr(),
						},
					},
				},
			},
			expectError: true,
		},
		"DependencySkipsGitTagsIfSourceRequiresNonPatches": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "B", Variant: "ubuntu"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{
							Name:      "A",
							Patchable: utility.FalsePtr(),
							DependsOn: []model.TaskUnitDependency{
								{Name: "B", Variant: "ubuntu"},
							},
						},
						{
							Name:       "B",
							GitTagOnly: utility.TruePtr(),
						},
					},
				},
			},
			expectError: true,
		},
		"DependencySkipsGitTagsIfNotAllowedForGitTags": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "B", Variant: "ubuntu"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{
							Name:      "A",
							Patchable: utility.FalsePtr(),
							DependsOn: []model.TaskUnitDependency{
								{Name: "B", Variant: "ubuntu"},
							},
						},
						{
							Name:           "B",
							AllowForGitTag: utility.FalsePtr(),
						},
					},
				},
			},
			expectError: true,
		},
		"DependencyIncludesGitTagsIfAllowed": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "B", Variant: "ubuntu"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{
							Name: "A",
							DependsOn: []model.TaskUnitDependency{
								{Name: "B", Variant: "ubuntu"},
							},
						},
						{
							Name:           "B",
							AllowForGitTag: utility.TruePtr(),
						},
					},
				},
			},
			expectError: false,
		},
		"DependencySkipsPatchIfSourceIncludesGitTags": {
			dependentTask:  model.TVPair{TaskName: "A", Variant: "ubuntu"},
			dependedOnTask: model.TVPair{TaskName: "B", Variant: "ubuntu"},
			buildVariants: []model.BuildVariant{
				{
					Name: "ubuntu",
					Tasks: []model.BuildVariantTaskUnit{
						{
							Name: "A",
							DependsOn: []model.TaskUnitDependency{
								{Name: "B", Variant: "ubuntu"},
							},
						},
						{
							Name:      "B",
							PatchOnly: utility.TruePtr(),
						},
					},
				},
			},
			expectError: true,
		},
	} {
		t.Run(testName, func(t *testing.T) {
			err := validateTVDependsOnTV(
				testCase.dependentTask,
				testCase.dependedOnTask,
				testCase.statuses,
				&model.Project{BuildVariants: testCase.buildVariants},
			)
			if testCase.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestParseS3PullParameters(t *testing.T) {
	for testName, testCase := range map[string]struct {
		expectError bool
		params      map[string]interface{}
	}{
		"PassesWithPopulatedParameters": {
			expectError: false,
			params: map[string]interface{}{
				"task":               "t",
				"from_build_variant": "bv",
			},
		},
		"PassesWithPopulatedTaskOnly": {
			expectError: false,
			params: map[string]interface{}{
				"task": "t",
			},
		},
		"FailsForEmptyParameters": {
			expectError: true,
			params:      map[string]interface{}{},
		},
		"FailsForNilParameters": {
			expectError: true,
		},
		"FailsForMissingTask": {
			expectError: true,
			params: map[string]interface{}{
				"from_build_variant": "bv",
			},
		},
		"FailsForNonStringTaskArgument": {
			expectError: true,
			params: map[string]interface{}{
				"task":               0,
				"from_build_variant": "bv",
			},
		},
		"FailsForNonStringBuildVariantArgument": {
			expectError: true,
			params: map[string]interface{}{
				"task":               "task",
				"from_build_variant": 0,
			},
		},
	} {
		t.Run(testName, func(t *testing.T) {
			cmd := model.PluginCommandConf{
				Command: evergreen.S3PullCommandName,
				Params:  testCase.params,
			}
			task, bv, err := parseS3PullParameters(cmd)
			if testCase.expectError {
				assert.Error(t, err)
				assert.Empty(t, task)
				assert.Empty(t, bv)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, testCase.params["task"], task)
				if fromBV, ok := testCase.params["from_build_variant"]; ok {
					assert.Equal(t, fromBV, bv)
				} else {
					assert.Empty(t, bv)
				}
			}
		})
	}
}

func TestValidateTaskGroupsInBV(t *testing.T) {
	tests := map[string]struct {
		project        model.Project
		expectErr      bool
		expectedErrMsg string
	}{
		"Task group before task": {
			project: model.Project{
				Tasks: []model.ProjectTask{
					{
						Name: "task1",
					},
					{
						Name: "task2",
					},
					{
						Name: "task3",
					},
				},
				TaskGroups: []model.TaskGroup{
					model.TaskGroup{
						Name:  "task1-and-task2",
						Tasks: []string{"task1", "task2"},
					},
				},
				BuildVariants: []model.BuildVariant{
					model.BuildVariant{
						Name: "ubuntu",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "task1-and-task2", IsGroup: true},
							{Name: "task1"},
						},
					},
				},
			},
			expectErr:      true,
			expectedErrMsg: "task 'task1' in build variant 'ubuntu' is already referenced in task group 'task1-and-task2'",
		},
		"Task group after task": {
			project: model.Project{
				Tasks: []model.ProjectTask{
					{
						Name: "task1",
					},
					{
						Name: "task2",
					},
					{
						Name: "task3",
					},
				},
				TaskGroups: []model.TaskGroup{
					model.TaskGroup{
						Name:  "task1-and-task2",
						Tasks: []string{"task1", "task2"},
					},
				},
				BuildVariants: []model.BuildVariant{
					model.BuildVariant{
						Name: "ubuntu",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "task2"},
							{Name: "task1-and-task2", IsGroup: true},
						},
					},
				},
			},
			expectErr:      true,
			expectedErrMsg: "task 'task2' in build variant 'ubuntu' is already referenced in task group 'task1-and-task2'",
		},
		"Task group and task not in task group": {
			project: model.Project{
				Tasks: []model.ProjectTask{
					{
						Name: "task1",
					},
					{
						Name: "task2",
					},
					{
						Name: "task3",
					},
				},
				TaskGroups: []model.TaskGroup{
					model.TaskGroup{
						Name:  "task1-and-task2",
						Tasks: []string{"task1", "task2"},
					},
				},
				BuildVariants: []model.BuildVariant{
					model.BuildVariant{
						Name: "ubuntu",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "task3"},
							{Name: "task1-and-task2", IsGroup: true},
						},
					},
				},
			},
			expectErr: false,
		},
		"No task group": {
			project: model.Project{
				Tasks: []model.ProjectTask{
					{
						Name: "task1",
					},
					{
						Name: "task2",
					},
					{
						Name: "task3",
					},
				},
				TaskGroups: []model.TaskGroup{
					model.TaskGroup{
						Name:  "task1-and-task2",
						Tasks: []string{"task1", "task2"},
					},
				},
				BuildVariants: []model.BuildVariant{
					model.BuildVariant{
						Name: "ubuntu",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "task3"},
							{Name: "task1"},
						},
					},
				},
			},
			expectErr:      true,
			expectedErrMsg: "task 'task1' in build variant 'ubuntu' is already referenced in task group 'task1-and-task2'",
		},
		"Multiple task group": {
			project: model.Project{
				Tasks: []model.ProjectTask{
					{
						Name: "task1",
					},
					{
						Name: "task2",
					},
					{
						Name: "task3",
					},
				},
				TaskGroups: []model.TaskGroup{
					model.TaskGroup{
						Name:  "task1-and-task2",
						Tasks: []string{"task1", "task2"},
					},
					{
						Name:  "task1-and-task3",
						Tasks: []string{"task1", "task3"},
					},
				},
				BuildVariants: []model.BuildVariant{
					model.BuildVariant{
						Name: "ubuntu",
						Tasks: []model.BuildVariantTaskUnit{
							{Name: "task1-and-task2", IsGroup: true},
							{Name: "task1-and-task3", IsGroup: true},
						},
					},
				},
			},
			expectErr: false,
		},
	}
	for testName, testCase := range tests {
		t.Run(testName, func(t *testing.T) {
			errs := ensureReferentialIntegrity(&testCase.project, nil, []string{}, []string{})
			if testCase.expectErr {
				assert.Equal(t, errs[0].Message, testCase.expectedErrMsg)
			} else {
				assert.Equal(t, len(errs), 0, "there was an error validating task group in build variant")
			}
		})
	}
}

func TestBVsWithTasksThatCallCommand(t *testing.T) {
	findCmdByDisplayName := func(cmds []model.PluginCommandConf, name string) *model.PluginCommandConf {
		for _, cmd := range cmds {
			if cmd.DisplayName == name {
				return &cmd
			}
		}
		return nil
	}
	cmd := evergreen.S3PullCommandName
	t.Run("CommandsIn", func(t *testing.T) {
		for testName, testCase := range map[string]struct {
			project                    model.Project
			expectedBVsToTasksWithCmds map[string]map[string][]model.PluginCommandConf
		}{
			"Task": {
				project: model.Project{
					Tasks: []model.ProjectTask{
						{
							Name: "setup",
							Commands: []model.PluginCommandConf{
								{
									DisplayName: "push_dir",
									Command:     evergreen.S3PushCommandName,
								},
							},
						}, {
							Name: "pull",
							Commands: []model.PluginCommandConf{
								{
									DisplayName: "pull_dir",
									Command:     evergreen.S3PullCommandName,
								},
							},
						}, {
							Name: "pull_twice",
							Commands: []model.PluginCommandConf{
								{
									DisplayName: "pull_dir1",
									Command:     evergreen.S3PullCommandName,
								},
								{
									DisplayName: "pull_dir2",
									Command:     evergreen.S3PullCommandName,
								},
							},
						}, {
							Name: "test",
							Commands: []model.PluginCommandConf{
								{
									DisplayName: "pull_dir_for_test",
									Command:     evergreen.S3PullCommandName,
									Variants:    []string{"rhel", "debian"},
								},
								{
									DisplayName: "generate_test",
									Command:     evergreen.GenerateTasksCommandName,
								},
							},
						}, {
							Name: "lint",
							Commands: []model.PluginCommandConf{
								{
									DisplayName: "generate_lint",
									Command:     evergreen.GenerateTasksCommandName,
								},
							},
						},
					},
					BuildVariants: []model.BuildVariant{
						{
							Name: "ubuntu",
							Tasks: []model.BuildVariantTaskUnit{
								{Name: "pull"},
							},
						},
						{
							Name: "rhel",
							Tasks: []model.BuildVariantTaskUnit{
								{Name: "test"},
								{Name: "pull_twice"},
							},
						}, {
							Name: "archlinux",
							Tasks: []model.BuildVariantTaskUnit{
								{Name: "lint"},
							},
						}, {
							Name: "debian",
							Tasks: []model.BuildVariantTaskUnit{
								{Name: "pull"},
								{Name: "test"},
								{Name: "lint"},
							},
						}, {
							Name: "fedora",
							Tasks: []model.BuildVariantTaskUnit{
								{Name: "pull"},
								{Name: "test"},
							},
						},
					},
				},
				expectedBVsToTasksWithCmds: map[string]map[string][]model.PluginCommandConf{
					"ubuntu": {
						"pull": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
					"rhel": {
						"test": {
							{
								DisplayName: "pull_dir_for_test",
								Command:     evergreen.S3PullCommandName,
							},
						},
						"pull_twice": {
							{
								DisplayName: "pull_dir1",
								Command:     evergreen.S3PullCommandName,
							},
							{
								DisplayName: "pull_dir2",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
					"debian": {
						"pull": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
						"test": {
							{
								DisplayName: "pull_dir_for_test",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
					"fedora": {
						"pull": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
				},
			},
			"TaskFunctionExpandsCommands": {
				project: model.Project{
					Functions: map[string]*model.YAMLCommandSet{
						"pull_func": &model.YAMLCommandSet{
							SingleCommand: &model.PluginCommandConf{
								Command:     evergreen.S3PullCommandName,
								DisplayName: "pull_dir",
							},
						},
						"test_func": &model.YAMLCommandSet{
							MultiCommand: []model.PluginCommandConf{
								{
									Command:     evergreen.S3PullCommandName,
									DisplayName: "pull_dir_for_test",
								}, {
									Command:     evergreen.GenerateTasksCommandName,
									DisplayName: "generate_test",
								},
							},
						},
					},
					Tasks: []model.ProjectTask{
						{
							Name: "setup",
							Commands: []model.PluginCommandConf{
								{
									Function: "pull_func",
								},
							},
						}, {
							Name: "test",
							Commands: []model.PluginCommandConf{
								{
									Function: "test_func",
								},
							},
						},
					},
					BuildVariants: []model.BuildVariant{
						{
							Name: "ubuntu",
							Tasks: []model.BuildVariantTaskUnit{
								{Name: "setup"},
							},
						},
						{
							Name: "rhel",
							Tasks: []model.BuildVariantTaskUnit{
								{Name: "test"},
							},
						},
					},
				},
				expectedBVsToTasksWithCmds: map[string]map[string][]model.PluginCommandConf{
					"ubuntu": {
						"setup": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
					"rhel": {
						"test": {
							{
								DisplayName: "pull_dir_for_test",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
				},
			},
			"Pre": {
				project: model.Project{
					Pre: &model.YAMLCommandSet{
						MultiCommand: []model.PluginCommandConf{
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
								Variants:    []string{"ubuntu", "rhel"},
							},
						},
					},
					Tasks: []model.ProjectTask{
						{Name: "test"},
					},
					BuildVariants: []model.BuildVariant{
						{
							Name: "ubuntu",
							Tasks: []model.BuildVariantTaskUnit{
								{Name: "test"},
							},
						},
						{Name: "rhel",
							Tasks: []model.BuildVariantTaskUnit{
								{Name: "test"},
							},
						}, {
							Name: "archlinux",
							Tasks: []model.BuildVariantTaskUnit{
								{Name: "test"},
							},
						},
					},
				},
				expectedBVsToTasksWithCmds: map[string]map[string][]model.PluginCommandConf{
					"ubuntu": {
						"test": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
					"rhel": {
						"test": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
				},
			},
			"Post": {
				project: model.Project{
					Post: &model.YAMLCommandSet{
						MultiCommand: []model.PluginCommandConf{
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
								Variants:    []string{"ubuntu", "rhel"},
							},
						},
					},
					Tasks: []model.ProjectTask{
						{Name: "test"},
					},
					BuildVariants: []model.BuildVariant{
						{
							Name: "ubuntu",
							Tasks: []model.BuildVariantTaskUnit{
								{Name: "test"},
							},
						},
						{Name: "rhel",
							Tasks: []model.BuildVariantTaskUnit{
								{Name: "test"},
							},
						}, {
							Name: "archlinux",
							Tasks: []model.BuildVariantTaskUnit{
								{Name: "test"},
							},
						},
					},
				},
				expectedBVsToTasksWithCmds: map[string]map[string][]model.PluginCommandConf{
					"ubuntu": {
						"test": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
					"rhel": {
						"test": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
				},
			},
			"SetupGroupInTaskGroup": {
				project: model.Project{
					TaskGroups: []model.TaskGroup{
						{
							Name: "test_group",
							SetupGroup: &model.YAMLCommandSet{
								MultiCommand: []model.PluginCommandConf{
									{
										DisplayName: "pull_dir",
										Command:     evergreen.S3PullCommandName,
										Variants:    []string{"ubuntu", "rhel"},
									},
								},
							},
							Tasks: []string{"test"},
						},
					},
					Tasks: []model.ProjectTask{
						{Name: "test"},
					},
					BuildVariants: []model.BuildVariant{
						{
							Name: "ubuntu",
							Tasks: []model.BuildVariantTaskUnit{
								{
									Name:    "test_group",
									IsGroup: true,
								},
							},
						},
						{Name: "rhel",
							Tasks: []model.BuildVariantTaskUnit{
								{
									Name:    "test_group",
									IsGroup: true,
								},
							},
						}, {
							Name: "archlinux",
							Tasks: []model.BuildVariantTaskUnit{
								{
									Name:    "test_group",
									IsGroup: true,
								},
							},
						},
					},
				},
				expectedBVsToTasksWithCmds: map[string]map[string][]model.PluginCommandConf{
					"ubuntu": {
						"test": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
					"rhel": {
						"test": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
				},
			},
			"SetupTaskInTaskGroup": {
				project: model.Project{
					TaskGroups: []model.TaskGroup{
						{
							Name: "test_group",
							SetupTask: &model.YAMLCommandSet{
								MultiCommand: []model.PluginCommandConf{
									{
										DisplayName: "pull_dir",
										Command:     evergreen.S3PullCommandName,
										Variants:    []string{"ubuntu", "rhel"},
									},
								},
							},
							Tasks: []string{"test"},
						},
					},
					Tasks: []model.ProjectTask{
						{Name: "test"},
					},
					BuildVariants: []model.BuildVariant{
						{
							Name: "ubuntu",
							Tasks: []model.BuildVariantTaskUnit{
								{
									Name:    "test_group",
									IsGroup: true,
								},
							},
						},
						{Name: "rhel",
							Tasks: []model.BuildVariantTaskUnit{
								{
									Name:    "test_group",
									IsGroup: true,
								},
							},
						}, {
							Name: "archlinux",
							Tasks: []model.BuildVariantTaskUnit{
								{
									Name:    "test_group",
									IsGroup: true,
								},
							},
						},
					},
				},
				expectedBVsToTasksWithCmds: map[string]map[string][]model.PluginCommandConf{
					"ubuntu": {
						"test": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
					"rhel": {
						"test": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
				},
			},
			"TasksInTaskGroup": {
				project: model.Project{
					TaskGroups: []model.TaskGroup{
						{
							Name:  "test_group",
							Tasks: []string{"test"},
						},
					},
					Tasks: []model.ProjectTask{
						{
							Name: "test",
							Commands: []model.PluginCommandConf{
								{
									Command:     evergreen.S3PullCommandName,
									DisplayName: "pull_dir",
									Variants:    []string{"ubuntu", "rhel"},
								},
							},
						},
					},
					BuildVariants: []model.BuildVariant{
						{
							Name: "ubuntu",
							Tasks: []model.BuildVariantTaskUnit{
								{
									Name:    "test_group",
									IsGroup: true,
								},
							},
						},
						{Name: "rhel",
							Tasks: []model.BuildVariantTaskUnit{
								{
									Name:    "test_group",
									IsGroup: true,
								},
							},
						}, {
							Name: "archlinux",
							Tasks: []model.BuildVariantTaskUnit{
								{
									Name:    "test_group",
									IsGroup: true,
								},
							},
						},
					},
				},
				expectedBVsToTasksWithCmds: map[string]map[string][]model.PluginCommandConf{
					"ubuntu": {
						"test": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
					"rhel": {
						"test": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
				},
			},
			"TeardownGroupInTaskGroup": {
				project: model.Project{
					TaskGroups: []model.TaskGroup{
						{
							Name: "test_group",
							TeardownGroup: &model.YAMLCommandSet{
								MultiCommand: []model.PluginCommandConf{
									{
										DisplayName: "pull_dir",
										Command:     evergreen.S3PullCommandName,
										Variants:    []string{"ubuntu", "rhel"},
									},
								},
							},
							Tasks: []string{"test"},
						},
					},
					Tasks: []model.ProjectTask{
						{Name: "test"},
					},
					BuildVariants: []model.BuildVariant{
						{
							Name: "ubuntu",
							Tasks: []model.BuildVariantTaskUnit{
								{
									Name:    "test_group",
									IsGroup: true,
								},
							},
						},
						{Name: "rhel",
							Tasks: []model.BuildVariantTaskUnit{
								{
									Name:    "test_group",
									IsGroup: true,
								},
							},
						}, {
							Name: "archlinux",
							Tasks: []model.BuildVariantTaskUnit{
								{
									Name:    "test_group",
									IsGroup: true,
								},
							},
						},
					},
				},
				expectedBVsToTasksWithCmds: map[string]map[string][]model.PluginCommandConf{
					"ubuntu": {
						"test": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
					"rhel": {
						"test": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
				},
			},
			"TeardownTaskInTaskGroup": {
				project: model.Project{
					TaskGroups: []model.TaskGroup{
						{
							Name: "test_group",
							TeardownTask: &model.YAMLCommandSet{
								MultiCommand: []model.PluginCommandConf{
									{
										DisplayName: "pull_dir",
										Command:     evergreen.S3PullCommandName,
										Variants:    []string{"ubuntu", "rhel"},
									},
								},
							},
							Tasks: []string{"test"},
						},
					},
					Tasks: []model.ProjectTask{
						{Name: "test"},
					},
					BuildVariants: []model.BuildVariant{
						{
							Name: "ubuntu",
							Tasks: []model.BuildVariantTaskUnit{
								{
									Name:    "test_group",
									IsGroup: true,
								},
							},
						}, {
							Name: "rhel",
							Tasks: []model.BuildVariantTaskUnit{
								{
									Name:    "test_group",
									IsGroup: true,
								},
							},
						}, {
							Name: "archlinux",
							Tasks: []model.BuildVariantTaskUnit{
								{
									Name:    "test_group",
									IsGroup: true,
								},
							},
						},
					},
				},
				expectedBVsToTasksWithCmds: map[string]map[string][]model.PluginCommandConf{
					"ubuntu": {
						"test": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
					"rhel": {
						"test": {
							{
								DisplayName: "pull_dir",
								Command:     evergreen.S3PullCommandName,
							},
						},
					},
				},
			},
		} {
			t.Run(testName, func(t *testing.T) {
				bvsToTasksWithCmds, _, err := bvsWithTasksThatCallCommand(&testCase.project, cmd)
				require.NoError(t, err)
				assert.Len(t, bvsToTasksWithCmds, len(testCase.expectedBVsToTasksWithCmds))
				for bv, expectedTasks := range testCase.expectedBVsToTasksWithCmds {
					assert.Contains(t, bvsToTasksWithCmds, bv)
					tasks := bvsToTasksWithCmds[bv]
					assert.Len(t, tasks, len(expectedTasks))
					for taskName, expectedCmds := range expectedTasks {
						assert.Contains(t, tasks, taskName)
						cmds := tasks[taskName]
						assert.Len(t, cmds, len(expectedCmds))
						for _, expectedCmd := range expectedCmds {
							cmd := findCmdByDisplayName(cmds, expectedCmd.DisplayName)
							require.NotNil(t, cmd)
							assert.Equal(t, expectedCmd.Command, cmd.Command)
						}
					}
				}
			})
		}
	})

	t.Run("MissingDefintiion", func(t *testing.T) {
		for testName, project := range map[string]model.Project{
			"ForTaskReferencedInBV": model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "ubuntu",
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name:    "test",
								IsGroup: true,
							},
						},
					},
				},
			},
			"ForTaskGroupReferencedInBV": model.Project{
				BuildVariants: []model.BuildVariant{
					{
						Name: "ubuntu",
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name:    "test_group",
								IsGroup: true,
							},
						},
					},
				},
			},
			"ForTaskReferencedInTaskGroupInBV": model.Project{
				TaskGroups: []model.TaskGroup{
					{
						Name:  "test_group",
						Tasks: []string{"test"},
					},
				},
				BuildVariants: []model.BuildVariant{
					{
						Name: "ubuntu",
						Tasks: []model.BuildVariantTaskUnit{
							{
								Name:    "test_group",
								IsGroup: true,
							},
						},
					},
				},
			},
		} {
			t.Run(testName, func(t *testing.T) {
				_, _, err := bvsWithTasksThatCallCommand(&project, cmd)
				assert.Error(t, err)
			})
		}
	})
}

func TestValidationErrorsAtLevel(t *testing.T) {
	t.Run("FindsWarningLevelErrors", func(t *testing.T) {
		errs := ValidationErrors([]ValidationError{
			{
				Level:   Warning,
				Message: "warning",
			}, {
				Level:   Error,
				Message: "error",
			},
		})
		foundErrs := errs.AtLevel(Warning)
		require.Len(t, foundErrs, 1)
		assert.Equal(t, errs[0], foundErrs[0])
	})
	t.Run("FindsErrorLevelErrors", func(t *testing.T) {
		errs := ValidationErrors([]ValidationError{
			{
				Level:   Warning,
				Message: "warning",
			}, {
				Level:   Error,
				Message: "error",
			},
		})
		foundErrs := errs.AtLevel(Error)
		require.Len(t, foundErrs, 1)
		assert.Equal(t, errs[1], foundErrs[0])
	})
	t.Run("ReturnsEmptyForNonexistent", func(t *testing.T) {
		errs := ValidationErrors([]ValidationError{})
		assert.Empty(t, errs.AtLevel(Error))
	})
	t.Run("ReturnsEmptyForNoMatch", func(t *testing.T) {
		errs := ValidationErrors([]ValidationError{
			{
				Level:   Warning,
				Message: "warning",
			},
		})
		assert.Empty(t, errs.AtLevel(Error))
	})
}
