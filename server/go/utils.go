package main

import (
	"encoding/json"
	"errors"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/aws/aws-sdk-go/service/dynamodb/expression"
	"io/ioutil"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// TODO: use actual worker ID
const DEFAULT_WORKER = "default_worker"

func GetProjectLocal(projectName string) (Project, error) {
	err := os.MkdirAll(env.DataDir, 0777)
	if err != nil {
		return Project{}, err
	}
	projectFilePath := path.Join(env.DataDir, projectName, "project.json")
	projectFileContents, err := ioutil.ReadFile(projectFilePath)
	if err != nil {
		return Project{}, err
	}
	project := Project{}
	err = json.Unmarshal(projectFileContents, &project)
	if err != nil {
		return Project{}, err
	}
	return project, nil
}

func GetProjectDatabase(projectName string) (Project, error) {
	result, err := svc.GetItem(&dynamodb.GetItemInput{
		TableName: aws.String("Project"),
		Key: map[string]*dynamodb.AttributeValue{
			"ProjectName": {
				S: aws.String(projectName),
			},
		},
	})
	if err != nil {
		return Project{}, err
	}
	project := Project{}
	err = dynamodbattribute.UnmarshalMap(result.Item, &project)
	if err != nil {
		return Project{}, err
	}
	return project, nil
}

func GetProject(projectName string) (Project, error) {
	if env.Database {
		return GetProjectDatabase(projectName)
	} else {
		return GetProjectLocal(projectName)
	}
}

func DeleteProjectLocal(projectName string) error {
	err := os.MkdirAll(env.DataDir, 0777)
	if err != nil {
		return err
	}
	projectFileDir := path.Join(env.DataDir, projectName)
	os.RemoveAll(projectFileDir)
	return nil
}

func DeleteProjectDatabase(projectName string) error {
	input := &dynamodb.DeleteItemInput{
		TableName: aws.String("Project"),
		Key: map[string]*dynamodb.AttributeValue{
			"ProjectName": {
				S: aws.String(projectName),
			},
		},
	}
	_, err := svc.DeleteItem(input)
	if err != nil {
		return err
	}
	return nil
}

func DeleteProject(projectName string) error {
	if env.Database {
		return DeleteProjectDatabase(projectName)
	} else {
		return DeleteProjectLocal(projectName)
	}
}

func GetTaskLocal(projectName string, index string) (Task, error) {
	taskPath := path.Join(env.DataDir, projectName, "tasks", index+".json")
	taskFileContents, err := ioutil.ReadFile(taskPath)
	if err != nil {
		return Task{}, err
	}
	task := Task{}
	err = json.Unmarshal(taskFileContents, &task)
	if err != nil {
		return Task{}, err
	}
	return task, nil
}

func GetTaskDatabase(projectName string, index string) (Task, error) {
	result, err := svc.GetItem(&dynamodb.GetItemInput{
		TableName: aws.String("Task"),
		Key: map[string]*dynamodb.AttributeValue{
			"ProjectName": {
				S: aws.String(projectName),
			},
			"index": {
				N: aws.String(index),
			},
		},
	})
	if err != nil {
		return Task{}, err
	}
	task := Task{}
	err = dynamodbattribute.UnmarshalMap(result.Item, &task)
	if err != nil {
		return Task{}, err
	}
	return task, nil
}

func GetTask(projectName string, index string) (Task, error) {
	if env.Database {
		return GetTaskDatabase(projectName, index)
	} else {
		return GetTaskLocal(projectName, index)
	}
}

func GetTasksInProjectLocal(projectName string) ([]Task, error) {
	projectTasksPath := path.Join(env.DataDir, projectName, "tasks")
	os.MkdirAll(projectTasksPath, 0777)
	tasksDirectoryContents, err := ioutil.ReadDir(projectTasksPath)
	if err != nil {
		return []Task{}, err
	}
	tasks := []Task{}
	for _, taskFile := range tasksDirectoryContents {
		if len(taskFile.Name()) > 5 &&
			path.Ext(taskFile.Name()) == ".json" {
			taskFileContents, err := ioutil.ReadFile(
				path.Join(projectTasksPath, taskFile.Name()))
			if err != nil {
				return []Task{}, err
			}
			task := Task{}
			err = json.Unmarshal(taskFileContents, &task)
			if err != nil {
				return []Task{}, err
			}
			tasks = append(tasks, task)
		}
	}
	// sort tasks by index
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].Index < tasks[j].Index
	})
	return tasks, nil
}

func GetTasksInProjectDatabase(projectName string) ([]Task, error) {
	filt := expression.Name("ProjectName").Equal(expression.Value(projectName))
	proj := expression.NamesList(expression.Name("ProjectName"), expression.Name("projectOptions"),
		expression.Name("index"), expression.Name("items"))
	expr, err := expression.NewBuilder().WithFilter(filt).WithProjection(proj).Build()
	params := &dynamodb.ScanInput{
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		FilterExpression:          expr.Filter(),
		ProjectionExpression:      expr.Projection(),
		TableName:                 aws.String("Task"),
	}
	// Make the DynamoDB Query API call
	result, err := svc.Scan(params)

	tasks := []Task{}
	for _, i := range result.Items {
		task := Task{}
		err = dynamodbattribute.UnmarshalMap(i, &task)
		if err != nil {
			return []Task{}, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func GetTasksInProject(projectName string) ([]Task, error) {
	if projectName == "" {
		return []Task{}, errors.New("Empty project name")
	}
	if env.Database {
		return GetTasksInProjectDatabase(projectName)
	} else {
		return GetTasksInProjectLocal(projectName)
	}
}

// Get the most recent assignment given the needed fields.
func GetAssignmentLocal(projectName string, taskIndex string, workerId string) (Assignment, error) {
	assignment := Assignment{}
	submissionsPath := path.Join(env.DataDir, projectName, "submissions",
		taskIndex, workerId)
	os.MkdirAll(submissionsPath, 0777)
	submissionsDirectoryContents, err := ioutil.ReadDir(submissionsPath)
	if err != nil {
		return Assignment{}, err
	}
	// directory contents should already be sorted, just need to remove all non-JSON
	submissionsDirectoryJSONs := []os.FileInfo{}
	for _, fi := range submissionsDirectoryContents {
		if path.Ext(fi.Name()) == ".json" {
			submissionsDirectoryJSONs = append(submissionsDirectoryJSONs, fi)
		}
	}
	// if any submissions exist, get the most recent one
	if len(submissionsDirectoryJSONs) > 0 {
		submissionFileContents, err := ioutil.ReadFile(path.Join(submissionsPath,
			submissionsDirectoryJSONs[len(submissionsDirectoryJSONs)-1].Name()))
		if err != nil {
			return Assignment{}, err
		}
		err = json.Unmarshal(submissionFileContents, &assignment)
		if err != nil {
			return Assignment{}, err
		}
	} else {
		assignmentPath := path.Join(env.DataDir, projectName, "assignments",
			taskIndex, workerId+".json")
		assignmentFileContents, err := ioutil.ReadFile(assignmentPath)
		if err != nil {
			return Assignment{}, err
		}
		err = json.Unmarshal(assignmentFileContents, &assignment)
		if err != nil {
			return Assignment{}, err
		}
	}
	return assignment, nil
}

// Get the most recent assignment given the needed fields.
func GetAssignmentDatabase(projectName string, taskIndex string, workerId string) (Assignment, error) {
	primaryKey := projectName + taskIndex + workerId
	filt := expression.Name("PrimaryKey").Equal(expression.Value(primaryKey))
	proj := expression.NamesList(expression.Name("PrimaryKey"), expression.Name("task"),
		expression.Name("workerId"), expression.Name("labels"),
		expression.Name("tracks"), expression.Name("events"),
		expression.Name("startTime"), expression.Name("submitTime"),
		expression.Name("numLabeledItems"), expression.Name("userAgent"),
		expression.Name("ipInfo"))
	expr, err := expression.NewBuilder().WithFilter(filt).WithProjection(proj).Build()
	params := &dynamodb.ScanInput{
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		FilterExpression:          expr.Filter(),
		ProjectionExpression:      expr.Projection(),
		TableName:                 aws.String("Submission"),
	}
	// Make the DynamoDB Query API call
	result, err := svc.Scan(params)
	items := result.Items
	assignment := Assignment{}
	if len(items) > 0 {
		item := items[0] //not sure this is the lastest, maybe change later
		err = dynamodbattribute.UnmarshalMap(item, &assignment)
		if err != nil {
			return Assignment{}, err
		}
	} else {
		result, err := svc.GetItem(&dynamodb.GetItemInput{
			TableName: aws.String("Assignment"),
			Key: map[string]*dynamodb.AttributeValue{
				"PrimaryKey": {
					S: aws.String(primaryKey),
				},
			},
		})
		if err != nil {
			return Assignment{}, err
		}
		err = dynamodbattribute.UnmarshalMap(result.Item, &assignment)
		if err != nil {
			return Assignment{}, err
		}
	}
	return assignment, nil
}

func GetAssignment(projectName string, taskIndex string, workerId string) (Assignment, error) {
	if env.Database {
		return GetAssignmentDatabase(projectName, taskIndex, workerId)
	} else {
		return GetAssignmentLocal(projectName, taskIndex, workerId)
	}
}

func CreateAssignment(projectName string, taskIndex string, workerId string) (Assignment, error) {
	task, err := GetTask(projectName, taskIndex)
	if err != nil {
		return Assignment{}, err
	}
	assignment := Assignment{
		Task:      task,
		WorkerId:  workerId,
		StartTime: recordTimestamp(),
	}
	assignment.Initialize()
	return assignment, nil
}

func GetDashboardContents(projectName string) (DashboardContents, error) {
	project, err := GetProject(projectName)
	if err != nil {
		return DashboardContents{}, err
	}
	tasks, err := GetTasksInProject(projectName)
	if err != nil {
		return DashboardContents{}, err
	}
	return DashboardContents{
		Project: project,
		Tasks:   tasks,
	}, nil
}

func GetHandlerUrl(itemType string, labelType string) string {
	switch itemType {
	case "image":
	    if labelType == "box2d" || labelType == "segmentation" || labelType == "lane" {
	        return "2d_labeling"
	    } else {
	        return "NO_VALID_HANDLER"
	    }
	case "video":
		if labelType == "box2d" || labelType == "segmentation" {
            return "2d_labeling"
        } else {
            return "NO_VALID_HANDLER"
        }
	case "pointcloud":
	    if labelType == "box3d" {
            return "3d_labeling"
        } else {
            return "NO_VALID_HANDLER"
        }
	}
	return "NO_VALID_HANDLER"
}

func recordTimestamp() int64 {
	// record timestamp in seconds
	return time.Now().Unix()
}

func formatTime(timestamp int64) string {
	t := time.Unix(timestamp, 0)
	return t.Format("2006-01-02_03-04-05")
}

func formatID(id int) string {
	str := strconv.Itoa(id)
	strLen := utf8.RuneCountInString(str)
	for i := 0; i < (4 - strLen); i += 1 {
		str = "0" + str
	}
	return str
}

func Exists(name string) bool {
	_, err := os.Stat(name)
	if os.IsNotExist(err) {
		return false
	} else {
		return true
	}
}

func Min(x, y int) int {
	if x < y {
		return x
	}
	return y
}

// Behavior is similar to path stem in Python
func PathStem(name string) string {
	name = path.Base(name)
	dotIndex := strings.LastIndex(name, ".")
	if dotIndex < 0 {
		return name
	} else {
		return name[:dotIndex]
	}
}

// check duplicated project name
// return false if duplicated
func CheckProjectNameLocal(projectName string) string {
	var newName = strings.Replace(projectName, " ", "_", -1)
	os.MkdirAll(env.DataDir, 0777)
	files, err := ioutil.ReadDir(env.DataDir)
	if err != nil {
		return newName
	}

	for _, f := range files {
		if PathStem(f.Name()) == newName {
			Error.Printf("Project Name \"%s\" already exists.", projectName)
			return ""
		}
	}
	return newName
}

// check duplicated project name
// return false if duplicated
func CheckProjectNameDatabase(projectName string) string {
	var newName = strings.Replace(projectName, " ", "_", -1)
	result, err := svc.GetItem(&dynamodb.GetItemInput{
		TableName: aws.String("Project"),
		Key: map[string]*dynamodb.AttributeValue{
			"ProjectName": {
				S: aws.String(newName),
			},
		},
	})
	if err != nil {
		return newName
	}
	if len(result.Item) != 0 {
		Error.Printf("Project Name \"%s\" already exists.", projectName)
		return ""
	}
	return newName
}

func CheckProjectName(projectName string) string {
	if env.Database {
		return CheckProjectNameDatabase(projectName)
	} else {
		return CheckProjectNameLocal(projectName)
	}
}

// default box2d category if category file is missing
var defaultBox2dCategories = []Category{
	{"person", nil},
	{"rider", nil},
	{"car", nil},
	{"truck", nil},
	{"bus", nil},
	{"train", nil},
	{"motor", nil},
	{"bike", nil},
	{"traffic sign", nil},
	{"traffic light", nil},
}

// default seg2d category if category file is missing
var defaultSeg2dCategories = []Category{
	{"void", []Category{
		{"unlabeled", nil},
		{"dynamic", nil},
		{"ego vehicle", nil},
		{"ground", nil},
		{"static", nil},
	}},
	{"flat", []Category{
		{"parking", nil},
		{"rail track", nil},
		{"road", nil},
		{"sidewalk", nil},
	}},
	{"construction", []Category{
		{"bridge", nil},
		{"building", nil},
		{"bus stop", nil},
		{"fence", nil},
		{"garage", nil},
		{"guard rail", nil},
		{"tunnel", nil},
		{"wall", nil},
	}},
	{"object", []Category{
		{"banner", nil},
		{"billboard", nil},
		{"fire hydrant", nil},
		{"lane divider", nil},
		{"mail box", nil},
		{"parking sign", nil},
		{"pole", nil},
		{"polegroup", nil},
		{"street light", nil},
		{"traffic cone", nil},
		{"traffic device", nil},
		{"traffic light", nil},
		{"traffic sign", nil},
		{"traffic sign frame", nil},
		{"trash can", nil},
	}},
	{"nature", []Category{
		{"terrain", nil},
		{"vegetation", nil},
	}},
	{"sky", []Category{
		{"sky", nil},
	}},
	{"human", []Category{
		{"person", nil},
		{"rider", nil},
	}},
	{"vehicle", []Category{
		{"bicycle", nil},
		{"bus", nil},
		{"car", nil},
		{"caravan", nil},
		{"motorcycle", nil},
		{"trailer", nil},
		{"train", nil},
		{"truck", nil},
	}},
}

// default lane2d category if category file is missing
var defaultLane2dCategories = []Category{
	{"road curb", nil},
	{"double white", nil},
	{"double yellow", nil},
	{"double other", nil},
	{"single white", nil},
	{"single yellow", nil},
	{"single other", nil},
	{"crosswalk", nil},
}

// default box2d attributes if attribute file is missing
var defaultBox2dAttributes = []Attribute{
	{"Occluded", "switch", "o",
		"", nil, nil, nil,
	},
	{"Truncated", "switch", "t",
		"", nil, nil, nil,
	},
	{"Traffic Light Color", "list", "", "t",
		[]string{"", "g", "y", "r"}, []string{"NA", "G", "Y", "R"},
		[]string{"white", "green", "yellow", "red"},
	},
}

// default attributes if attribute file is missing
// to avoid uncaught type error in Javascript file
var dummyAttribute = []Attribute{
	{"", "", "",
		"", nil, nil, nil,
	},
}
