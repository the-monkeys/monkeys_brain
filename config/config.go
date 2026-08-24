package config

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

type TheMonkeysGateway struct {
	HTTPS        string `mapstructure:"HTTPS"`
	HTTP         string `mapstructure:"HTTP"`
	HTTPPort     int    `mapstructure:"http_port"`
	InternalPort int    `mapstructure:"internal_port"`
}
type Microservices struct {
	TheMonkeysAuthz            string `mapstructure:"the_monkeys_authz"`
	AuthzPort                  int    `mapstructure:"authz_port"`
	AuthzInternalPort          int    `mapstructure:"authz_internal_port"`
	TheMonkeysBlog             string `mapstructure:"the_monkeys_blog"`
	BlogPort                   int    `mapstructure:"blog_port"`
	BlogInternalPort           int    `mapstructure:"blog_internal_port"`
	TheMonkeysUser             string `mapstructure:"the_monkeys_user"`
	UserPort                   int    `mapstructure:"user_port"`
	UserInternalPort           int    `mapstructure:"user_internal_port"`
	TheMonkeysFileStore        string `mapstructure:"the_monkeys_storage"`
	StoragePort                int    `mapstructure:"storage_port"`
	StorageInternalPort        int    `mapstructure:"storage_internal_port"`
	TheMonkeysNotification     string `mapstructure:"the_monkeys_notification"`
	NotificationPort           int    `mapstructure:"notification_port"`
	NotificationInternalPort   int    `mapstructure:"notification_internal_port"`
	TheMonkeysCache            string `mapstructure:"the_monkeys_cache"`
	TheMonkeysAIEngine         string `mapstructure:"the_monkeys_ai_engine"`
	AIEnginePort               int    `mapstructure:"ai_engine_port"`
	AIEngineInternalPort       int    `mapstructure:"ai_engine_internal_port"`
	AIEngineHealthPort         int    `mapstructure:"ai_engine_health_port"`
	AIEngineHealthInternalPort int    `mapstructure:"ai_engine_health_internal_port"`
	TheMonkeysActivity         string `mapstructure:"the_monkeys_activity"`
	ActivityPort               int    `mapstructure:"activity_port"`
	ActivityInternalPort       int    `mapstructure:"activity_internal_port"`
	TheMonkeysEvents           string `mapstructure:"the_monkeys_events"`
	EventsPort                 int    `mapstructure:"events_port"`
	EventsInternalPort         int    `mapstructure:"events_internal_port"`
	TheMonkeysGroups           string `mapstructure:"the_monkeys_groups"`
	GroupsPort                 int    `mapstructure:"groups_port"`
	GroupsInternalPort         int    `mapstructure:"groups_internal_port"`
}

type Database struct {
	DBUsername   string `mapstructure:"db_username"`
	DBPassword   string `mapstructure:"db_password"`
	DBHost       string `mapstructure:"db_host"`
	DBPort       int    `mapstructure:"db_port"`
	InternalPort int    `mapstructure:"internal_port"`
	DBName       string `mapstructure:"db_name"`
}

type Postgresql struct {
	PrimaryDB Database `mapstructure:"primary_db"`
	Replica1  Database `mapstructure:"replica_1"`
}

type JWT struct {
	SecretKey string `mapstructure:"secret_key"`
}

type Opensearch struct {
	Address       string `mapstructure:"address"`
	Host          string `mapstructure:"os_host"`
	Username      string `mapstructure:"os_username"`
	Password      string `mapstructure:"os_password"`
	HttpPort      int    `mapstructure:"http_port"`
	TransportPort int    `mapstructure:"transport_port"`
}

type Email struct {
	SMTPAddress  string `mapstructure:"smtp_address"`
	SMTPMail     string `mapstructure:"smtp_mail"`
	SMTPPassword string `mapstructure:"smtp_password"`
	SMTPHost     string `mapstructure:"smtp_host"`
}

type Gmail struct {
	SMTPAddress  string `mapstructure:"smtp_address"`
	SMTPMail     string `mapstructure:"smtp_mail"`
	SMTPPassword string `mapstructure:"smtp_password"`
	SMTPHost     string `mapstructure:"smtp_host"`
}

type Authentication struct {
	EmailVerificationAddr string `mapstructure:"email_verification_addr"`
}

type RabbitMQ struct {
	Protocol               string   `mapstructure:"protocol"`
	Host                   string   `mapstructure:"host"`
	Port                   string   `mapstructure:"port"`
	InternalPort           string   `mapstructure:"internal_port"`
	ManagementPort         string   `mapstructure:"managementport"`
	ManagementInternalPort string   `mapstructure:"management_internal_port"`
	Username               string   `mapstructure:"username"`
	Password               string   `mapstructure:"password"`
	VirtualHost            string   `mapstructure:"virtual_host"`
	Exchange               string   `mapstructure:"exchange"`
	Queues                 []string `yaml:"queues"`
	RoutingKeys            []string `yaml:"routingKeys"`

	// Dead Letter Queue configuration for reliable message delivery.
	// Messages that are nacked or exceed retry limits are routed here.
	DLXExchange string `mapstructure:"dlx_exchange"`
	DLQQueue    string `mapstructure:"dlq_queue"`
	DLQKey      string `mapstructure:"dlq_key"`
	MaxRetries  int    `mapstructure:"max_retries"`
}

type Keys struct {
	MediaStack            string `mapstructure:"mediastack"`
	NewsApi               string `mapstructure:"newsapi"`
	HindustanTimes        string `mapstructure:"hindustantimes"`
	GitHubToken           string `mapstructure:"github_token"`
	AdminSecretKey        string `mapstructure:"admin_secret_key"`
	SystemKey             string `mapstructure:"system_key"` // Key for system-level operations
	RazorpayKeyID         string `mapstructure:"razorpay_key_id"`
	RazorpaySecret        string `mapstructure:"razorpay_secret"`
	RazorpayWebhookSecret string `mapstructure:"razorpay_webhook_secret"`
}

type SEO struct {
	Enabled                 bool   `mapstructure:"enabled"`
	GoogleIndexingAPIKey    string `mapstructure:"google_indexing_api_key"`
	GoogleIndexingAPI       string `mapstructure:"google_indexing_api"`
	SearchConsoleSitemapURL string `mapstructure:"search_console_sitemap_url"`
	BaseURL                 string `mapstructure:"base_url"`
	GoogleCredentialsFile   string `mapstructure:"google_credentials_file"`
}

type GoogleOAuth2 struct {
	RedirectURL  string   `mapstructure:"redirect_url"`
	ClientID     string   `mapstructure:"client_id"`
	ClientSecret string   `mapstructure:"client_secret"`
	Scope        []string `mapstructure:"scope"`
	Endpoint     string   `mapstructure:"endpoint"`
}

// Minio holds object storage configuration
type Minio struct {
	Endpoint      string `mapstructure:"endpoint"`
	AccessKey     string `mapstructure:"access_key"`
	SecretKey     string `mapstructure:"secret_key"`
	Bucket        string `mapstructure:"bucket_name"`
	UseSSL        bool   `mapstructure:"use_ssl"`
	CDNURL        string `mapstructure:"cdn_url"`
	PublicBaseURL string `mapstructure:"public_base_url"`
	// Remote sync settings
	RemoteEndpoint   string `mapstructure:"remote_endpoint"`
	RemoteAccessKey  string `mapstructure:"remote_access_key"`
	RemoteSecretKey  string `mapstructure:"remote_secret_key"`
	RemoteBucketName string `mapstructure:"remote_bucket_name"`
	// Bidirectional sync toggles.
	// Set to false to disable the corresponding periodic sync loop.
	// Both default to true (Viper zero-value for bool is false,
	// so the env var must be explicitly set to "false" to disable).
	EnableFSToMinioSync bool `mapstructure:"enable_fs_to_minio_sync"`
	EnableMinioToFSSync bool `mapstructure:"enable_minio_to_fs_sync"`
}

type Cors struct {
	AllowedOriginExp string `mapstructure:"allowed_origin_regexp"`
	UseTempCors      bool   `mapstructure:"use_temp_cors"`
}

type FreeRangeNotify struct {
	BaseURL      string `mapstructure:"base_url"`
	APIKey       string `mapstructure:"api_key"`
	AppID        string `mapstructure:"app_id"`
	EmailEnabled bool   `mapstructure:"email_enabled"`
	SSEPublicURL string `mapstructure:"sse_public_url"`
	DevEmail     string `mapstructure:"dev_email"` // Override all email recipients in dev
}

type Redis struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
	PoolSize int    `mapstructure:"pool_size"`
	MaxIdle  int    `mapstructure:"max_idle"`
}

type Config struct {
	TheMonkeysGateway TheMonkeysGateway `mapstructure:"the_monkeys_gateway"`
	Microservices     Microservices     `mapstructure:"microservices"`
	Postgresql        Postgresql        `mapstructure:"postgresql"`
	JWT               JWT               `mapstructure:"jwt"`
	Opensearch        Opensearch        `mapstructure:"opensearch"`
	Email             Email             `mapstructure:"email"`
	Gmail             Gmail             `mapstructure:"gmail"`
	Authentication    Authentication    `mapstructure:"authentication"`
	RabbitMQ          RabbitMQ          `mapstructure:"rabbitMQ"`
	Keys              Keys              `mapstructure:"keys"`
	GoogleOAuth2      GoogleOAuth2      `mapstructure:"google_oauth2"`
	Cors              Cors              `mapstructure:"cors"`
	Redis             Redis             `mapstructure:"redis"`
	Minio             Minio             `mapstructure:"minio"`
	SEO               SEO               `mapstructure:"seo"`
	FreeRangeNotify   FreeRangeNotify   `mapstructure:"freerangenotify"`
	AppEnv            string            `mapstructure:"app_env"`
}

func GetConfig() (*Config, error) {
	log := zap.S()
	// Load .env file if it exists
	if err := godotenv.Load(".env"); err != nil {
		log.Warnf("No .env file found or error loading .env file: %v", err)
	} else {
		log.Debug("Successfully loaded .env file")
	}

	// Configure Viper for environment variables
	viper.SetEnvPrefix("") // No prefix for env vars
	viper.AutomaticEnv()   // Ensures environment variables are loaded

	// Set environment variable key replacer to handle nested structs
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Bind environment variables to config keys
	bindEnvVars()

	// Comment out YAML reading to test environment variables only
	// viper.SetConfigName("config")
	// viper.SetConfigType("yaml") // Ensure this is yaml
	// viper.AddConfigPath("./config")

	config := &Config{}

	// Comment out YAML config reading for testing
	// Binding struct to config
	// if err := viper.ReadInConfig(); err != nil {
	//  log.Errorf("Error reading config file, %v", err)
	//  return config, err
	// }

	if err := viper.Unmarshal(config); err != nil {
		log.Errorf("Unable to decode into struct, %v", err)
		return config, err
	}

	// Handle array environment variables manually
	handleArrayEnvVars(config)

	log.Debug("Configuration loaded from environment variables only")
	return config, nil
}

// bindEnvVars manually binds environment variables to viper keys
func bindEnvVars() {
	// Gateway
	viper.BindEnv("the_monkeys_gateway.HTTPS", "THE_MONKEYS_GATEWAY_HTTPS")
	viper.BindEnv("the_monkeys_gateway.HTTP", "THE_MONKEYS_GATEWAY_HTTP")
	viper.BindEnv("the_monkeys_gateway.http_port", "THE_MONKEYS_GATEWAY_HTTP_PORT")
	viper.BindEnv("the_monkeys_gateway.internal_port", "THE_MONKEYS_GATEWAY_INTERNAL_PORT")
	viper.BindEnv("app_env", "APP_ENV")

	// Microservices
	viper.BindEnv("microservices.the_monkeys_authz", "MICROSERVICES_THE_MONKEYS_AUTHZ")
	viper.BindEnv("microservices.authz_port", "MICROSERVICES_AUTHZ_PORT")
	viper.BindEnv("microservices.authz_internal_port", "MICROSERVICES_AUTHZ_INTERNAL_PORT")
	viper.BindEnv("microservices.the_monkeys_blog", "MICROSERVICES_THE_MONKEYS_BLOG")
	viper.BindEnv("microservices.blog_port", "MICROSERVICES_BLOG_PORT")
	viper.BindEnv("microservices.blog_internal_port", "MICROSERVICES_BLOG_INTERNAL_PORT")
	viper.BindEnv("microservices.the_monkeys_user", "MICROSERVICES_THE_MONKEYS_USER")
	viper.BindEnv("microservices.user_port", "MICROSERVICES_USER_PORT")
	viper.BindEnv("microservices.user_internal_port", "MICROSERVICES_USER_INTERNAL_PORT")
	viper.BindEnv("microservices.the_monkeys_storage", "MICROSERVICES_THE_MONKEYS_STORAGE")
	viper.BindEnv("microservices.storage_port", "MICROSERVICES_STORAGE_PORT")
	viper.BindEnv("microservices.storage_internal_port", "MICROSERVICES_STORAGE_INTERNAL_PORT")
	viper.BindEnv("microservices.the_monkeys_notification", "MICROSERVICES_THE_MONKEYS_NOTIFICATION")
	viper.BindEnv("microservices.notification_port", "MICROSERVICES_NOTIFICATION_PORT")
	viper.BindEnv("microservices.notification_internal_port", "MICROSERVICES_NOTIFICATION_INTERNAL_PORT")
	viper.BindEnv("microservices.the_monkeys_cache", "MICROSERVICES_THE_MONKEYS_CACHE")
	viper.BindEnv("microservices.the_monkeys_ai_engine", "MICROSERVICES_THE_MONKEYS_AI_ENGINE")
	viper.BindEnv("microservices.ai_engine_port", "MICROSERVICES_AI_ENGINE_PORT")
	viper.BindEnv("microservices.ai_engine_internal_port", "MICROSERVICES_AI_ENGINE_INTERNAL_PORT")
	viper.BindEnv("microservices.ai_engine_health_port", "MICROSERVICES_AI_ENGINE_HEALTH_PORT")
	viper.BindEnv("microservices.ai_engine_health_internal_port", "MICROSERVICES_AI_ENGINE_HEALTH_INTERNAL_PORT")
	viper.BindEnv("microservices.the_monkeys_activity", "MICROSERVICES_THE_MONKEYS_ACTIVITY")
	viper.BindEnv("microservices.activity_port", "MICROSERVICES_ACTIVITY_PORT")
	viper.BindEnv("microservices.activity_internal_port", "MICROSERVICES_ACTIVITY_INTERNAL_PORT")
	viper.BindEnv("microservices.the_monkeys_events", "MICROSERVICES_THE_MONKEYS_EVENTS")
	viper.BindEnv("microservices.events_port", "MICROSERVICES_EVENTS_PORT")
	viper.BindEnv("microservices.events_internal_port", "MICROSERVICES_EVENTS_INTERNAL_PORT")
	viper.BindEnv("microservices.the_monkeys_groups", "MICROSERVICES_THE_MONKEYS_GROUPS")
	viper.BindEnv("microservices.groups_port", "MICROSERVICES_GROUPS_PORT")
	viper.BindEnv("microservices.groups_internal_port", "MICROSERVICES_GROUPS_INTERNAL_PORT")

	// PostgreSQL
	viper.BindEnv("postgresql.primary_db.db_username", "POSTGRESQL_PRIMARY_DB_DB_USERNAME")
	viper.BindEnv("postgresql.primary_db.db_password", "POSTGRESQL_PRIMARY_DB_DB_PASSWORD")
	viper.BindEnv("postgresql.primary_db.db_host", "POSTGRESQL_PRIMARY_DB_DB_HOST")
	viper.BindEnv("postgresql.primary_db.db_port", "POSTGRESQL_PRIMARY_DB_DB_PORT")
	viper.BindEnv("postgresql.primary_db.internal_port", "POSTGRESQL_PRIMARY_DB_INTERNAL_PORT")
	viper.BindEnv("postgresql.primary_db.db_name", "POSTGRESQL_PRIMARY_DB_DB_NAME")

	viper.BindEnv("postgresql.replica_1.db_username", "POSTGRESQL_REPLICA_1_DB_USERNAME")
	viper.BindEnv("postgresql.replica_1.db_password", "POSTGRESQL_REPLICA_1_DB_PASSWORD")
	viper.BindEnv("postgresql.replica_1.db_host", "POSTGRESQL_REPLICA_1_DB_HOST")
	viper.BindEnv("postgresql.replica_1.db_port", "POSTGRESQL_REPLICA_1_DB_PORT")
	viper.BindEnv("postgresql.replica_1.db_name", "POSTGRESQL_REPLICA_1_DB_NAME")

	// JWT
	viper.BindEnv("jwt.secret_key", "JWT_SECRET_KEY")

	// OpenSearch
	viper.BindEnv("opensearch.address", "OPENSEARCH_ADDRESS")
	viper.BindEnv("opensearch.os_host", "OPENSEARCH_OS_HOST")
	viper.BindEnv("opensearch.os_username", "OPENSEARCH_OS_USERNAME")
	viper.BindEnv("opensearch.os_password", "OPENSEARCH_OS_PASSWORD")
	viper.BindEnv("opensearch.http_port", "OPENSEARCH_HTTP_PORT")
	viper.BindEnv("opensearch.transport_port", "OPENSEARCH_TRANSPORT_PORT")

	// Email
	viper.BindEnv("email.smtp_address", "EMAIL_SMTP_ADDRESS")
	viper.BindEnv("email.smtp_mail", "EMAIL_SMTP_MAIL")
	viper.BindEnv("email.smtp_password", "EMAIL_SMTP_PASSWORD")
	viper.BindEnv("email.smtp_host", "EMAIL_SMTP_HOST")

	// Gmail
	viper.BindEnv("gmail.smtp_address", "GMAIL_SMTP_ADDRESS")
	viper.BindEnv("gmail.smtp_mail", "GMAIL_SMTP_MAIL")
	viper.BindEnv("gmail.smtp_password", "GMAIL_SMTP_PASSWORD")
	viper.BindEnv("gmail.smtp_host", "GMAIL_SMTP_HOST")

	// Authentication
	viper.BindEnv("authentication.email_verification_addr", "AUTHENTICATION_EMAIL_VERIFICATION_ADDR")

	// Google OAuth2
	viper.BindEnv("google_oauth2.redirect_url", "GOOGLE_OAUTH2_REDIRECT_URL")
	viper.BindEnv("google_oauth2.client_id", "GOOGLE_OAUTH2_CLIENT_ID")
	viper.BindEnv("google_oauth2.client_secret", "GOOGLE_OAUTH2_CLIENT_SECRET")
	viper.BindEnv("google_oauth2.endpoint", "GOOGLE_OAUTH2_ENDPOINT")

	// RabbitMQ
	viper.BindEnv("rabbitMQ.protocol", "RABBITMQ_PROTOCOL")
	viper.BindEnv("rabbitMQ.host", "RABBITMQ_HOST")
	viper.BindEnv("rabbitMQ.port", "RABBITMQ_PORT")
	viper.BindEnv("rabbitMQ.internal_port", "RABBITMQ_INTERNAL_PORT")
	viper.BindEnv("rabbitMQ.managementport", "RABBITMQ_MANAGEMENT_PORT")
	viper.BindEnv("rabbitMQ.management_internal_port", "RABBITMQ_MANAGEMENT_INTERNAL_PORT")
	viper.BindEnv("rabbitMQ.username", "RABBITMQ_USERNAME")
	viper.BindEnv("rabbitMQ.password", "RABBITMQ_PASSWORD")
	viper.BindEnv("rabbitMQ.virtual_host", "RABBITMQ_VIRTUAL_HOST")
	viper.BindEnv("rabbitMQ.exchange", "RABBITMQ_EXCHANGE")
	viper.BindEnv("rabbitMQ.dlx_exchange", "RABBITMQ_DLX_EXCHANGE")
	viper.BindEnv("rabbitMQ.dlq_queue", "RABBITMQ_DLQ_QUEUE")
	viper.BindEnv("rabbitMQ.dlq_key", "RABBITMQ_DLQ_KEY")
	viper.BindEnv("rabbitMQ.max_retries", "RABBITMQ_MAX_RETRIES")

	// Keys
	viper.BindEnv("keys.mediastack", "KEYS_MEDIASTACK")
	viper.BindEnv("keys.newsapi", "KEYS_NEWSAPI")
	viper.BindEnv("keys.hindustantimes", "KEYS_HINDUSTANTIMES")
	viper.BindEnv("keys.github_token", "KEYS_GITHUB_TOKEN")
	viper.BindEnv("keys.admin_secret_key", "KEYS_ADMIN_SECRET_KEY")
	viper.BindEnv("keys.system_key", "KEYS_SYSTEM_KEY")
	viper.BindEnv("keys.razorpay_key_id", "KEYS_RAZORPAY_KEY_ID")
	viper.BindEnv("keys.razorpay_secret", "KEYS_RAZORPAY_SECRET")
	viper.BindEnv("keys.razorpay_webhook_secret", "KEYS_RAZORPAY_WEBHOOK_SECRET")

	// CORS
	viper.BindEnv("cors.allowed_origin_regexp", "CORS_ALLOWED_ORIGIN_REGEXP")
	viper.BindEnv("cors.use_temp_cors", "CORS_USE_TEMP_CORS")

	// Redis
	viper.BindEnv("redis.host", "REDIS_HOST")
	viper.BindEnv("redis.port", "REDIS_PORT")
	viper.BindEnv("redis.password", "REDIS_PASSWORD")
	viper.BindEnv("redis.db", "REDIS_DB")
	viper.BindEnv("redis.pool_size", "REDIS_POOL_SIZE")
	viper.BindEnv("redis.max_idle", "REDIS_MAX_IDLE")

	// MinIO
	viper.BindEnv("minio.endpoint", "MINIO_ENDPOINT")
	viper.BindEnv("minio.access_key", "MINIO_ACCESS_KEY")
	viper.BindEnv("minio.secret_key", "MINIO_SECRET_KEY")
	viper.BindEnv("minio.bucket_name", "MINIO_BUCKET_NAME")
	viper.BindEnv("minio.use_ssl", "MINIO_USE_SSL")
	viper.BindEnv("minio.cdn_url", "MINIO_CDN_URL")
	viper.BindEnv("minio.public_base_url", "MINIO_PUBLIC_BASE_URL")
	viper.BindEnv("minio.remote_endpoint", "MINIO_REMOTE_ENDPOINT")
	viper.BindEnv("minio.remote_access_key", "MINIO_REMOTE_ACCESS_KEY")
	viper.BindEnv("minio.remote_secret_key", "MINIO_REMOTE_SECRET_KEY")
	viper.BindEnv("minio.remote_bucket_name", "MINIO_REMOTE_BUCKET_NAME")
	viper.BindEnv("minio.enable_fs_to_minio_sync", "MINIO_ENABLE_FS_TO_MINIO_SYNC")
	viper.BindEnv("minio.enable_minio_to_fs_sync", "MINIO_ENABLE_MINIO_TO_FS_SYNC")

	// SEO
	viper.BindEnv("seo.enabled", "SEO_ENABLED")
	viper.BindEnv("seo.google_indexing_api_key", "SEO_GOOGLE_INDEXING_API_KEY")
	viper.BindEnv("seo.google_indexing_api", "SEO_GOOGLE_INDEXING_API")
	viper.BindEnv("seo.search_console_sitemap_url", "SEO_SEARCH_CONSOLE_SITEMAP_URL")
	viper.BindEnv("seo.base_url", "SEO_BASE_URL")
	viper.BindEnv("seo.google_credentials_file", "SEO_GOOGLE_CREDENTIALS_FILE")

	// FreeRangeNotify
	viper.BindEnv("freerangenotify.base_url", "FRN_BASE_URL")
	viper.BindEnv("freerangenotify.api_key", "FRN_API_KEY")
	viper.BindEnv("freerangenotify.app_id", "FRN_APP_ID")
	viper.BindEnv("freerangenotify.email_enabled", "FRN_EMAIL_ENABLED")
	viper.BindEnv("freerangenotify.sse_public_url", "FRN_SSE_PUBLIC_URL")
	viper.BindEnv("freerangenotify.dev_email", "FRN_DEV_EMAIL")
}

// handleArrayEnvVars manually handles array environment variables
func handleArrayEnvVars(config *Config) {
	// Handle RabbitMQ Queues
	if queuesStr := os.Getenv("RABBITMQ_QUEUES"); queuesStr != "" {
		config.RabbitMQ.Queues = strings.Split(queuesStr, ",")
		// Trim whitespace from each queue name
		for i, queue := range config.RabbitMQ.Queues {
			config.RabbitMQ.Queues[i] = strings.TrimSpace(queue)
		}
	}

	// Handle RabbitMQ Routing Keys
	if routingKeysStr := os.Getenv("RABBITMQ_ROUTING_KEYS"); routingKeysStr != "" {
		config.RabbitMQ.RoutingKeys = strings.Split(routingKeysStr, ",")
		// Trim whitespace from each routing key
		for i, key := range config.RabbitMQ.RoutingKeys {
			config.RabbitMQ.RoutingKeys[i] = strings.TrimSpace(key)
		}
	}

	// Handle Google OAuth2 Scopes (if needed from env)
	if scopesStr := os.Getenv("GOOGLE_OAUTH2_SCOPES"); scopesStr != "" {
		config.GoogleOAuth2.Scope = strings.Split(scopesStr, ",")
		// Trim whitespace from each scope
		for i, scope := range config.GoogleOAuth2.Scope {
			config.GoogleOAuth2.Scope[i] = strings.TrimSpace(scope)
		}
	}
}
