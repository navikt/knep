package config

type Config struct {
	BindAddress    string
	StatsProjectID string
	StatsDatasetID string
	StatsTableID   string
	InCluster      bool
}
