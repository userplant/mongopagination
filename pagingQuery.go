package mongopagination

import (
	"context"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Error constants
const (
	PageLimitError         = "page or limit cannot be less than 0"
	FilterInAggregateError = "you cannot use filter in aggregate query but you can pass multiple filter as param in aggregate function"
	NilFilterError         = "filter query cannot be nil"
)

// PagingQuery struct for holding mongo
// connection, filter needed to apply
// filter data with page, limit, sort key
// and sort value
type pagingQuery struct {
	Collection  *mongo.Collection
	SortField   string
	Project     interface{}
	FilterQuery interface{}
	SortValue   int
	LimitCount  int64
	PageCount   int64
}

// AutoGenerated is to bind Aggregate query result data
type AutoGenerated struct {
	Total []struct {
		Count int64 `json:"count"`
	} `json:"total"`
	Data []bson.Raw `json:"data"`
}

// PagingQuery is an interface that provides list of function
// you can perform on pagingQuery
type PagingQuery interface {
	// Find set the filter for query results.
	Find() (paginatedData *PaginatedData, err error)

	Aggregate(criteria ...interface{}) (paginatedData *PaginatedData, err error)

	// Select used to enable fields which should be retrieved.
	Select(selector interface{}) PagingQuery

	Filter(selector interface{}) PagingQuery
	Limit(limit int64) PagingQuery
	Page(page int64) PagingQuery
	Sort(sortField string, sortValue int) PagingQuery
}

// New is to construct PagingQuery object with mongo.Database and collection name
func New(collection *mongo.Collection) PagingQuery {
	return &pagingQuery{
		Collection: collection,
	}
}

// Select helps you to add projection on query
func (paging *pagingQuery) Select(selector interface{}) PagingQuery {
	paging.Project = selector
	return paging
}

// Filter function is to add filter for mongo query
func (paging *pagingQuery) Filter(criteria interface{}) PagingQuery {
	paging.FilterQuery = criteria
	return paging
}

// Limit is to add limit for pagination
func (paging *pagingQuery) Limit(limit int64) PagingQuery {
	if limit < 1 {
		paging.LimitCount = 10
	} else {
		paging.LimitCount = limit
	}
	return paging
}

// Page is to specify which page to serve in mongo paginated result
func (paging *pagingQuery) Page(page int64) PagingQuery {
	if page < 1 {
		paging.PageCount = 1
	} else {
		paging.PageCount = page
	}
	return paging
}

// Sort is to sor mongo result by certain key
func (paging *pagingQuery) Sort(sortField string, sortValue int) PagingQuery {
	paging.SortField = sortField
	paging.SortValue = sortValue
	return paging
}

// validateQuery query is to check if user has added certain required params or not
func (paging *pagingQuery) validateQuery() error {
	if paging.LimitCount <= 0 || paging.PageCount <= 0 {
		return errors.New(PageLimitError)
	}
	return nil
}

// Aggregate help you to paginate mongo pipeline query
// it returns PaginatedData struct and  error if any error
// occurs during document query
func (paging *pagingQuery) Aggregate(filters ...interface{}) (paginatedData *PaginatedData, err error) {
	// checking if user added required params
	if err := paging.validateQuery(); err != nil {
		return nil, err
	}
	if paging.FilterQuery != nil {
		return nil, errors.New(FilterInAggregateError)
	}

	var aggregationFilter []bson.M
	// combining user sent queries
	for _, filter := range filters {
		aggregationFilter = append(aggregationFilter, filter.(bson.M))
	}
	skip := getSkip(paging.PageCount, paging.LimitCount)

	// making facet aggregation pipeline for result and total document count
	facet := bson.M{"$facet": bson.M{
		"data": []bson.M{
			{"$sort": bson.M{"createdAt": -1}},
			{"$skip": skip},
			{"$limit": paging.LimitCount},
		},
		"total": []bson.M{{"$count": "count"}},
	},
	}
	aggregationFilter = append(aggregationFilter, facet)
	diskUse := true
	opt := &options.AggregateOptions{
		AllowDiskUse: &diskUse,
	}

	cursor, err := paging.Collection.Aggregate(context.Background(), aggregationFilter, opt)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())
	var docs []AutoGenerated
	for cursor.Next(context.Background()) {
		var document *AutoGenerated
		if err := cursor.Decode(&document); err == nil {
			docs = append(docs, *document)
		}
	}

	var data []bson.Raw
	var aggCount int64

	if len(docs) > 0 && len(docs[0].Data) > 0 {
		aggCount = docs[0].Total[0].Count
		data = docs[0].Data
	}
	paginationInfoChan := make(chan *Paginator, 1)
	Paging(paging, paginationInfoChan, true, aggCount)
	paginationInfo := <-paginationInfoChan
	result := PaginatedData{
		Pagination: *paginationInfo.PaginationData(),
		Data:       data,
	}
	return &result, nil
}

// Find returns two value pagination data with document queried from mongodb and
// error if any error occurs during document query
func (paging *pagingQuery) Find() (paginatedData *PaginatedData, err error) {

	if err := paging.validateQuery(); err != nil {
		return nil, err
	}
	if paging.FilterQuery == nil {
		return nil, errors.New(NilFilterError)
	}

	// get Pagination Info
	paginationInfoChan := make(chan *Paginator, 1)
	Paging(paging, paginationInfoChan, false, 0)

	// set options for sorting and skipping
	skip := getSkip(paging.PageCount, paging.LimitCount)
	opt := &options.FindOptions{
		Skip:  &skip,
		Limit: &paging.LimitCount,
	}
	if paging.Project != nil {
		opt.SetProjection(paging.Project)
	}
	if paging.SortField != "" && paging.SortValue != 0 {
		opt.SetSort(bson.D{{paging.SortField, paging.SortValue}})
	}
	cursor, err := paging.Collection.Find(context.Background(), paging.FilterQuery, opt)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())
	var docs []bson.Raw
	for cursor.Next(context.Background()) {
		var document *bson.Raw
		if err := cursor.Decode(&document); err == nil {
			docs = append(docs, *document)
		}
	}
	paginationInfo := <-paginationInfoChan
	result := PaginatedData{
		Pagination: *paginationInfo.PaginationData(),
		Data:       docs,
	}
	return &result, nil
}

// PaginatedData struct holds data and
// pagination detail
type PaginatedData struct {
	Data       []bson.Raw     `json:"data"`
	Pagination PaginationData `json:"pagination"`
}

// getSkip return calculated skip value for query
func getSkip(page, limit int64) (skip int64) {
	if page > 0 {
		skip = (page - 1) * limit
	} else {
		skip = page
	}
	return
}
