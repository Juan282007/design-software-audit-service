package config

import (
	"fmt"
	"os"
	"time"
)

type Common struct{ DatabaseURL, LogLevel, ServiceEnv string }
type QueryAPI struct {
	Common
	Port, IAMJWKSURL, IAMJWTIssuer, CursorSecret, ReadinessAddress string
	ReadinessInterval                                              time.Duration
}
type Worker struct {
	Common
	MessageBrokerURL, BrokerTopicPattern, BrokerConsumerGroup string
	AuditEventsExchange                                       string
	MaxRetries                                                int
	RetryDelayMilliseconds                                    int
	ReadinessAddress                                          string
	ReadinessInterval                                         time.Duration
}

func LoadQueryAPI() (QueryAPI, error) {
	c := QueryAPI{Common: common(), Port: value("PORT", "8080"), IAMJWKSURL: os.Getenv("IAM_JWKS_URL"), IAMJWTIssuer: os.Getenv("IAM_JWT_ISSUER"), CursorSecret: os.Getenv("AUDIT_CURSOR_SECRET"), ReadinessAddress: value("AUDIT_QUERY_READINESS_ADDR", ":8081"), ReadinessInterval: milliseconds("READINESS_CHECK_INTERVAL_MS", 5000)}
	for _, p := range []struct{ name, value string }{{"DATABASE_URL", c.DatabaseURL}, {"IAM_JWKS_URL", c.IAMJWKSURL}, {"IAM_JWT_ISSUER", c.IAMJWTIssuer}, {"AUDIT_CURSOR_SECRET", c.CursorSecret}} {
		if p.value == "" {
			return QueryAPI{}, missing(p.name)
		}
	}
	if c.ReadinessInterval <= 0 {
		return QueryAPI{}, fmt.Errorf("invalid readiness configuration")
	}
	return c, nil
}
func LoadWorker() (Worker, error) {
	c := Worker{Common: common(), MessageBrokerURL: os.Getenv("MESSAGE_BROKER_URL"), BrokerTopicPattern: os.Getenv("BROKER_TOPIC_PATTERN"), BrokerConsumerGroup: os.Getenv("BROKER_CONSUMER_GROUP"), AuditEventsExchange: value("AUDIT_EVENTS_EXCHANGE", "audit-events"), MaxRetries: intValue("WORKER_MAX_RETRIES", 3), RetryDelayMilliseconds: intValue("WORKER_RETRY_DELAY_MS", 5000), ReadinessAddress: value("AUDIT_WORKER_READINESS_ADDR", ":8082"), ReadinessInterval: milliseconds("READINESS_CHECK_INTERVAL_MS", 5000)}
	for _, p := range []struct{ name, value string }{{"DATABASE_URL", c.DatabaseURL}, {"MESSAGE_BROKER_URL", c.MessageBrokerURL}, {"BROKER_TOPIC_PATTERN", c.BrokerTopicPattern}, {"BROKER_CONSUMER_GROUP", c.BrokerConsumerGroup}} {
		if p.value == "" {
			return Worker{}, missing(p.name)
		}
	}
	if c.BrokerTopicPattern != "#" {
		return Worker{}, fmt.Errorf("invalid configuration: BROKER_TOPIC_PATTERN must be #")
	}
	if c.MaxRetries < 0 || c.RetryDelayMilliseconds < 1 || c.ReadinessInterval <= 0 {
		return Worker{}, fmt.Errorf("invalid worker retry configuration")
	}
	return c, nil
}
func milliseconds(n string, d int) time.Duration {
	return time.Duration(intValue(n, d)) * time.Millisecond
}
func common() Common {
	return Common{os.Getenv("DATABASE_URL"), value("LOG_LEVEL", "info"), value("SERVICE_ENV", "development")}
}
func value(n, d string) string {
	if v := os.Getenv(n); v != "" {
		return v
	}
	return d
}
func intValue(n string, d int) int {
	if v := os.Getenv(n); v != "" {
		var parsed int
		if _, err := fmt.Sscanf(v, "%d", &parsed); err == nil {
			return parsed
		}
		return -1
	}
	return d
}
func missing(n string) error { return fmt.Errorf("missing required configuration: %s", n) }
