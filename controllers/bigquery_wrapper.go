package controllers

import (
	"context"
	"errors"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/googleapi"
)

type BigQuery struct {
	Client        *bigquery.Client
	DestProjectID string
	DestDatasetID string
	DestTableID   string
}

func NewBigQuery(ctx context.Context, projectID, datasetID, tableID string) (*BigQuery, error) {
	bqClient, err := bigquery.NewClient(ctx, bigquery.DetectProjectID)
	if err != nil {
		return nil, err
	}

	if err := createAllowlistStatsTableIfNotExists(ctx, bqClient, projectID, datasetID, tableID); err != nil {
		return nil, err
	}

	return &BigQuery{
		Client:        bqClient,
		DestProjectID: projectID,
		DestDatasetID: datasetID,
		DestTableID:   tableID,
	}, nil
}

func createAllowlistStatsTableIfNotExists(ctx context.Context, bqClient *bigquery.Client, projectID, datasetID, tableID string) error {
	schema := bigquery.Schema{
		{Name: "team", Type: bigquery.StringFieldType},
		{Name: "namespace", Type: bigquery.StringFieldType},
		{Name: "service", Type: bigquery.StringFieldType},
		{Name: "allowlist", Type: bigquery.JSONFieldType},
		{Name: "created_at", Type: bigquery.TimestampFieldType},
	}

	metadata := &bigquery.TableMetadata{
		Schema: schema,
	}

	ds := bqClient.DatasetInProject(projectID, datasetID)
	err := ds.Table(tableID).Create(ctx, metadata)
	var e *googleapi.Error
	if ok := errors.As(err, &e); ok {
		if e.Code == 409 {
			// already exists
			return nil
		}
	}

	return nil
}
