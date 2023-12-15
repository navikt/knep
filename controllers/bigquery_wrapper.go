package controllers

import (
	"context"
	"encoding/json"
	"errors"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/googleapi"
	corev1 "k8s.io/api/core/v1"
)

type BigQuery struct {
	client        *bigquery.Client
	destProjectID string
	destDatasetID string
	destTableID   string
}

type allowListTableEntry struct {
	PodName   string                 `json:"podname"`
	Team      string                 `json:"team"`
	Namespace string                 `json:"namespace"`
	Service   string                 `json:"service"`
	Allowlist bigquery.NullJSON      `json:"allowlist"`
	Created   bigquery.NullTimestamp `json:"created"`
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
		client:        bqClient,
		destProjectID: projectID,
		destDatasetID: datasetID,
		destTableID:   tableID,
	}, nil
}

func createAllowlistStatsTableIfNotExists(ctx context.Context, bqClient *bigquery.Client, projectID, datasetID, tableID string) error {
	schema := bigquery.Schema{
		{Name: "created", Type: bigquery.TimestampFieldType, Required: true},
		{Name: "podname", Type: bigquery.StringFieldType, Required: true},
		{Name: "namespace", Type: bigquery.StringFieldType, Required: true},
		{Name: "team", Type: bigquery.StringFieldType},
		{Name: "service", Type: bigquery.StringFieldType},
		{Name: "allowlist", Type: bigquery.JSONFieldType},
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

func (bq *BigQuery) persistAllowlistStats(ctx context.Context, allowStruct allowIPFQDN, pod corev1.Pod) error {
	table := bq.client.DatasetInProject(bq.destProjectID, bq.destDatasetID).Table(bq.destTableID)

	allowBytes, err := json.Marshal(allowStruct)
	if err != nil {
		return err
	}

	tableEntry := allowListTableEntry{
		PodName:   pod.Name,
		Team:      pod.Spec.ServiceAccountName,
		Namespace: pod.Namespace,
		Service:   getServiceTypeFromPodSpec(pod),
		Allowlist: bigquery.NullJSON{JSONVal: string(allowBytes), Valid: string(allowBytes) != ""},
		Created:   bigquery.NullTimestamp{Timestamp: pod.CreationTimestamp.Time, Valid: true},
	}

	inserter := table.Inserter()
	return inserter.Put(ctx, tableEntry)
}

func getServiceTypeFromPodSpec(pod corev1.Pod) string {
	if serviceType, ok := pod.Labels["app"]; ok && serviceType == "jupyterhub" {
		return serviceType
	}

	if serviceType, ok := pod.Labels["release"]; ok && serviceType == "airflow" {
		return serviceType
	}

	return ""
}
