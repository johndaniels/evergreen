package testresult

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/evergreen-ci/evergreen"
	"github.com/evergreen-ci/evergreen/db"
	mgobson "github.com/evergreen-ci/evergreen/db/mgo/bson"
	_ "github.com/evergreen-ci/evergreen/testutil"
	adb "github.com/mongodb/anser/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// FindOne returns one test result that satisfies the query. Returns nil if no tasks match.
func findOne(query db.Q) (*TestResult, error) {
	test := &TestResult{}
	err := db.FindOneQ(Collection, query, &test)
	if adb.ResultsNotFound(err) {
		return nil, nil
	}
	return test, err
}

type TestResultSuite struct {
	suite.Suite
	tests []TestResult
}

func TestTestResultSuite(t *testing.T) {
	suite.Run(t, new(TestResultSuite))
}

func (s *TestResultSuite) SetupTest() {
	err := db.Clear(Collection)
	s.Require().NoError(err)

	s.tests = []TestResult{}
	for i := 0; i < 5; i++ {
		s.tests = append(s.tests, TestResult{
			ID:              mgobson.NewObjectId(),
			Status:          "pass",
			TestFile:        fmt.Sprintf("file-%d", i),
			DisplayTestName: fmt.Sprintf("display-%d", i),
			GroupID:         fmt.Sprintf("group-%d", i),
			URL:             fmt.Sprintf("url-%d", i),
			URLRaw:          fmt.Sprintf("urlraw-%d", i),
			LogID:           fmt.Sprintf("logid-%d", i),
			LineNum:         i,
			ExitCode:        i,
			StartTime:       float64(i),
			EndTime:         float64(i),
			TaskID:          fmt.Sprintf("taskid-%d", i),
			Execution:       i,
		})
	}

	for _, t := range s.tests {
		err := t.Insert()
		s.Require().NoError(err)
	}

	additionalTests := []TestResult{}
	for i := 5; i < 10; i++ {
		additionalTests = append(additionalTests, TestResult{
			ID:              mgobson.NewObjectId(),
			Status:          "pass",
			TestFile:        fmt.Sprintf("file-%d", i),
			DisplayTestName: fmt.Sprintf("display-%d", i),
			GroupID:         fmt.Sprintf("group-%d", i),
			URL:             fmt.Sprintf("url-%d", i),
			URLRaw:          fmt.Sprintf("urlraw-%d", i),
			LogID:           fmt.Sprintf("logid-%d", i),
			LineNum:         i,
			ExitCode:        i,
			StartTime:       float64(i),
			EndTime:         float64(i),
			TaskID:          "taskid-5",
			Execution:       5,
		})
	}

	for _, t := range additionalTests {
		err := t.Insert()
		s.Require().NoError(err)
	}
}

func (s *TestResultSuite) TestInsertTestResultForTask() {
	taskID := "taskid-10"
	execution := 3
	i := 10
	t := TestResult{
		ID:              mgobson.NewObjectId(),
		TaskID:          taskID,
		Execution:       execution,
		Status:          "pass",
		TestFile:        fmt.Sprintf("file-%d", i),
		DisplayTestName: fmt.Sprintf("display-%d", i),
		GroupID:         fmt.Sprintf("group-%d", i),
		URL:             fmt.Sprintf("url-%d", i),
		URLRaw:          fmt.Sprintf("urlraw-%d", i),
		LogID:           fmt.Sprintf("logid-%d", i),
		LineNum:         i,
		ExitCode:        i,
		StartTime:       float64(i),
		EndTime:         float64(i),
	}
	err := InsertMany([]TestResult{t})
	s.NoError(err)
	find, err := FindByTaskIDAndExecution(taskID, execution)
	s.NoError(err)
	s.Len(find, 1)
}

func (s *TestResultSuite) TestInsertManyTestResultsForTask() {
	taskID := "taskid-25"
	execution := 3
	toInsert := []TestResult{}
	for i := 20; i < 30; i++ {
		toInsert = append(toInsert, TestResult{
			ID:        mgobson.NewObjectId(),
			TaskID:    taskID,
			Execution: execution,
		})
	}
	err := InsertMany(toInsert)
	s.NoError(err)
	find, err := FindByTaskIDAndExecution(taskID, execution)
	s.NoError(err)
	s.Len(find, 10)
}

func (s *TestResultSuite) TestInsertTestResultForTaskEmptyTaskShouldErr() {
	taskID := ""
	execution := 3
	i := 10
	t := TestResult{
		ID:              mgobson.NewObjectId(),
		TaskID:          taskID,
		Execution:       execution,
		Status:          "pass",
		TestFile:        fmt.Sprintf("file-%d", i),
		DisplayTestName: fmt.Sprintf("file-%d", i),
		GroupID:         fmt.Sprintf("group-%d", i),
		URL:             fmt.Sprintf("url-%d", i),
		URLRaw:          fmt.Sprintf("urlraw-%d", i),
		LogID:           fmt.Sprintf("logid-%d", i),
		LineNum:         i,
		ExitCode:        i,
		StartTime:       float64(i),
		EndTime:         float64(i),
	}
	err := InsertMany([]TestResult{t})
	s.Error(err)
	find, err := FindByTaskIDAndExecution(taskID, execution)
	s.NoError(err)
	s.Len(find, 0)
}

func (s *TestResultSuite) TestInsert() {
	for i, t := range s.tests {
		test, err := findOne(db.Query(bson.M{
			"_id": t.ID,
		}))
		s.NoError(err)
		s.Equal("pass", test.Status)
		s.Equal(fmt.Sprintf("file-%d", i), test.TestFile)
		s.Equal(fmt.Sprintf("display-%d", i), test.DisplayTestName)
		s.Equal(fmt.Sprintf("group-%d", i), test.GroupID)
		s.Equal(fmt.Sprintf("url-%d", i), test.URL)
		s.Equal(fmt.Sprintf("urlraw-%d", i), test.URLRaw)
		s.Equal(fmt.Sprintf("logid-%d", i), test.LogID)
		s.Equal(i, test.LineNum)
		s.Equal(i, test.ExitCode)
		s.Equal(float64(i), test.StartTime)
		s.Equal(float64(i), test.EndTime)
		s.Equal(fmt.Sprintf("taskid-%d", i), test.TaskID)
		s.Equal(i, test.Execution)
	}

	test, err := findOne(db.Query(bson.M{
		"_id": 100,
	}))
	s.Nil(test)
	s.NoError(err)
}

func (s *TestResultSuite) TestFindByTaskIDAndExecution() {
	tests, err := FindByTaskIDAndExecution("taskid-5", 5)
	s.NoError(err)
	s.Len(tests, 5)
	for i, test := range tests {
		s.Equal("pass", test.Status)
		s.Equal(fmt.Sprintf("file-%d", i+5), test.TestFile)
		s.Equal(fmt.Sprintf("display-%d", i+5), test.DisplayTestName)
		s.Equal(fmt.Sprintf("group-%d", i+5), test.GroupID)
		s.Equal(fmt.Sprintf("url-%d", i+5), test.URL)
		s.Equal(fmt.Sprintf("logid-%d", i+5), test.LogID)
	}

	tests, err = FindByTaskIDAndExecution("taskid-1", 1)
	s.NoError(err)
	s.Len(tests, 1)

	tests, err = FindByTaskIDAndExecution("taskid-5", 100)
	s.NoError(err)
	s.Len(tests, 0)

	tests, err = FindByTaskIDAndExecution("taskid-100", 5)
	s.NoError(err)
	s.Len(tests, 0)

	tests, err = FindByTaskIDAndExecution("taskid-100", 100)
	s.NoError(err)
	s.Len(tests, 0)
}

func TestDeleteWithLimit(t *testing.T) {
	env := evergreen.GetEnvironment()
	ctx, cancel := env.Context()
	defer cancel()

	t.Run("DetectsOutOfBounds", func(t *testing.T) {
		assert.Panics(t, func() {
			_, _ = DeleteWithLimit(ctx, env, time.Now(), 200*1000)
		})
		assert.NotPanics(t, func() {
			_, _ = DeleteWithLimit(ctx, env, time.Now(), 1)
		})
	})
	t.Run("QueryValidation", func(t *testing.T) {
		require.NoError(t, db.Clear(Collection))
		require.NoError(t, (&TestResult{ID: mgobson.ObjectIdHex(primitive.NewObjectIDFromTimestamp(time.Now().Add(time.Hour)).Hex())}).Insert())
		require.NoError(t, (&TestResult{ID: mgobson.ObjectIdHex(primitive.NewObjectIDFromTimestamp(time.Now().Add(-time.Hour)).Hex())}).Insert())

		num, err := db.Count(Collection, bson.M{})
		require.NoError(t, err)
		assert.Equal(t, 2, num)

		num, err = DeleteWithLimit(ctx, env, time.Now(), 2)
		assert.NoError(t, err)
		assert.Equal(t, 1, num)

		num, err = db.Count(Collection, bson.M{})
		require.NoError(t, err)
		assert.Equal(t, 1, num)
	})
	t.Run("Parallel", func(t *testing.T) {
		require.NoError(t, db.Clear(Collection))
		for i := 0; i < 10000; i++ {
			if i%2 == 0 {
				require.NoError(t, (&TestResult{ID: mgobson.ObjectIdHex(primitive.NewObjectIDFromTimestamp(time.Now().Add(time.Hour)).Hex())}).Insert())
			} else {
				require.NoError(t, (&TestResult{ID: mgobson.ObjectIdHex(primitive.NewObjectIDFromTimestamp(time.Now().Add(-time.Hour)).Hex())}).Insert())
			}
		}
		num, err := db.Count(Collection, bson.M{})
		require.NoError(t, err)
		assert.Equal(t, 10000, num)

		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, delErr := DeleteWithLimit(ctx, env, time.Now(), 1000)
				require.NoError(t, delErr)
			}()
		}
		wg.Wait()

		num, err = db.Count(Collection, bson.M{})
		require.NoError(t, err)
		assert.Equal(t, 5000, num)
	})
}
