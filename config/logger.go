package config

type Logger struct {
	Path               string
	Level              string
	ReportCaller       bool `mapstructure:"report-caller"`
	DebugClientPayload bool `mapstructure:"debug-client-payload"`
}
