package statswriter

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"cloud.google.com/go/bigquery"
	"github.com/navikt/knep/pkg/hostmap"
	"google.golang.org/api/googleapi"
	corev1 "k8s.io/api/core/v1"
)

type AllowListStatistics struct {
	HostMap hostmap.AllowIPFQDN
	Pod     corev1.Pod
}

type BigQuery struct {
	ProjectID string
	DatasetID string
	TableID   string
}

type allowListTableEntry struct {
	PodName   string                 `json:"podname"`
	Team      string                 `json:"team"`
	Namespace string                 `json:"namespace"`
	Service   string                 `json:"service"`
	Allowlist bigquery.NullJSON      `json:"allowlist"`
	Created   bigquery.NullTimestamp `json:"created"`
}

func Run(ctx context.Context, sink BigQuery, statisticsChan chan AllowListStatistics, logger *slog.Logger) {
	bqClient, err := bigquery.NewClient(ctx, bigquery.DetectProjectID)
	if err != nil {
		logger.Error("unable to create bigquery client", "error", err)
		return
	}

	if err := createAllowlistStatsTableIfNotExists(ctx, bqClient, sink.ProjectID, sink.DatasetID, sink.TableID); err != nil {
		logger.Error("unable to create statistics table in bigquery", "error", err)
		return
	}
	if err := bqClient.Close(); err != nil {
		logger.Error("unable to close bigquery client", "error", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case allowStats := <-statisticsChan:
			if err := persistAllowlistStats(ctx, sink, allowStats.HostMap, allowStats.Pod); err != nil {
				logger.Error("persisting allowlist stats", "error", err, "podname", allowStats.Pod.Name, "namespace", allowStats.Pod.Namespace)
			}
		}
	}
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

func persistAllowlistStats(ctx context.Context, sink BigQuery, allowStruct any, pod corev1.Pod) error {
	bqClient, err := bigquery.NewClient(ctx, bigquery.DetectProjectID)
	if err != nil {
		return err
	}
	defer bqClient.Close()

	table := bqClient.DatasetInProject(sink.ProjectID, sink.DatasetID).Table(sink.TableID)

	allowBytes, err := json.Marshal(allowStruct)
	if err != nil {
		return err
	}

	service, team := getServiceTypeAndTeamFromPodSpec(pod)
	tableEntry := allowListTableEntry{
		PodName:   pod.Name,
		Team:      team,
		Namespace: pod.Namespace,
		Service:   service,
		Allowlist: bigquery.NullJSON{JSONVal: string(allowBytes), Valid: string(allowBytes) != ""},
		Created:   bigquery.NullTimestamp{Timestamp: pod.CreationTimestamp.Time, Valid: true},
	}

	return table.Inserter().Put(ctx, tableEntry)
}

func getServiceTypeAndTeamFromPodSpec(pod corev1.Pod) (string, string) {
	if serviceType, ok := pod.Labels["app"]; ok && serviceType == "jupyterhub" {
		team := ""
		if teamName, ok := pod.Labels["team"]; ok {
			team = teamName
		}
		return serviceType, team
	}

	return "airflow", pod.Spec.ServiceAccountName
}
