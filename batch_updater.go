package dynamodb

import (
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"log"
	"reflect"
)

type BatchUpdater struct {
	DB        *dynamodb.DynamoDB
	tableName string
	Map    func(ctx context.Context, model interface{}) (interface{}, error)
	keys   []string
}

func NewBatchUpdaterById(database *dynamodb.DynamoDB, tableName string, modelType reflect.Type, fieldName string, keys []string, options ...func(context.Context, interface{}) (interface{}, error)) *BatchUpdater {
	var mp func(context.Context, interface{}) (interface{}, error)
	if len(options) >= 1 {
		mp = options[0]
	}
	if len(fieldName) == 0 {
		_, idName, _ := FindIdField(modelType)
		fieldName = idName
	}
	return &BatchUpdater{Map: mp, DB: database,tableName: tableName, keys: keys}
}

func NewBatchUpdater(database *dynamodb.DynamoDB, tableName string, modelType reflect.Type, keys []string, options ...func(context.Context, interface{}) (interface{}, error)) *BatchUpdater {
	return NewBatchUpdaterById(database, tableName, modelType, "", keys, options...)
}

func (w *BatchUpdater) Write(ctx context.Context, models interface{}) ([]int, []int, error) {
	successIndices := make([]int, 0)
	failIndices := make([]int, 0)
	s := reflect.ValueOf(models)

	var er1 error
	if w.Map != nil {
		m2, er0 := w.Map(ctx, models)
		if er0 != nil {
			return successIndices, failIndices, er0
		}
		_, _, er1 = UpdateMany(ctx, w.DB, w.tableName, w.keys, m2)
	} else {
		_, _, er1 = UpdateMany(ctx, w.DB, w.tableName, w.keys, models)
	}
	if er1 == nil {
		for i := 0; i < s.Len(); i++ {
			successIndices = append(successIndices, i)
		}
		return successIndices, failIndices, er1
	}
	return successIndices, failIndices, er1
}

func TransactionUpdate(ctx context.Context, db *dynamodb.DynamoDB, data []interface{}, tableName string, keys []string) (*dynamodb.TransactWriteItemsOutput, error) {
	var listTransaction = make([]*dynamodb.TransactWriteItem, 0)
	for _, d := range data {
		ids := getIdValueFromModel(d, keys)

		isExit, err := Exist(ctx, db, tableName, keys, ids)
		if !isExit {
			return nil, err
		}

		keyMap, err := buildKeyMap(keys, ids)
		if err != nil {
			return nil, err
		}

		av, _ := dynamodbattribute.MarshalMap(d)
		expressionValues, expressionNames, expression := BuildUpdate(av, keys)
		update := &dynamodb.Update{
			ConditionExpression:       nil,
			ExpressionAttributeNames:  expressionNames,
			ExpressionAttributeValues: expressionValues,
			Key:                       keyMap,
			TableName:                 &tableName,
			UpdateExpression:          &expression,
		}
		transaction := &dynamodb.TransactWriteItem{
			Update: update,
		}
		listTransaction = append(listTransaction, transaction)
	}

	input := &dynamodb.TransactWriteItemsInput{
		TransactItems: listTransaction,
	}

	err := input.Validate()
	if err != nil {
		return nil, err
	}

	rs, err := db.TransactWriteItems(input)
	if err != nil {
		return nil, err
	}
	return rs, nil
}

func CheckKeys(k string, keys []string) bool {
	for _, key := range keys {
		if key == k {
			return true
		}
	}
	return false
}

func UpdateMany(ctx context.Context, db *dynamodb.DynamoDB, tableName string, keys []string, models interface{}) (interface{}, interface{}, error) {
	arr := make([]interface{}, 0)
	modelsType := reflect.TypeOf(models)
	insertedFails := reflect.New(modelsType).Interface()
	switch reflect.TypeOf(models).Kind() {
	case reflect.Slice:
		values := reflect.ValueOf(models)
		if values.Len() == 0 {
			return insertedFails, insertedFails, nil
		}
		for i := 0; i < values.Len(); i++ {
			arr = append(arr, values.Index(i).Interface())
		}
	}

	rs, err := TransactionUpdate(ctx, db, arr, tableName, keys)
	if err != nil {
		return nil, nil, err
	}
	log.Println(rs)
	return nil, nil, err
}

func BuildUpdate(av map[string]*dynamodb.AttributeValue, keys []string) (map[string]*dynamodb.AttributeValue, map[string]*string, string) {
	experssionValues := make(map[string]*dynamodb.AttributeValue)
	expressionNames := make(map[string]*string)
	updateExperssion := "set "
	for i, v := range av {
		if CheckKeys(i, keys) {
			continue
		}
		key := fmt.Sprintf(":%s1", i)
		experssionValues[key] = v
		keyName := fmt.Sprintf("#%s", i)
		keyNamePtr := i
		expressionNames[keyName] = &keyNamePtr
		str := fmt.Sprintf("%s = %s ,", keyName, key)
		updateExperssion += str
	}
	updateExperssion = updateExperssion[:len(updateExperssion)-1]
	return experssionValues, expressionNames, updateExperssion
}
