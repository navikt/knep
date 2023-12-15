package config

type Config struct {
	BindAddress    string
	StatsProjectID string
	StatsDatasetID string
	StatsTableID   string
	CertPath       string
	InCluster      bool
}
