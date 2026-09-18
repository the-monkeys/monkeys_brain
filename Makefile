# include .env
# export

# PSQLUSER := $(shell yq e '.postgresql.primary_db.db_username' config/config.yml)
# PSQLPASS := $(shell yq e '.postgresql.primary_db.db_password' config/config.yml)
# PSQLHOST := $(shell yq e '.postgresql.primary_db.db_host' config/config.yml)
# PSQLPORT := $(shell yq e '.postgresql.primary_db.db_port' config/config.yml)
# PSQLDB := $(shell yq e '.postgresql.primary_db.db_name' config/config.yml)

proto:
	protoc microservices/the_monkeys_gateway/internal/**/pb/*.proto --go_out=. --go-grpc_out=.
	protoc microservices/the_monkeys_authz/internal/pb/*.proto --go_out=. --go-grpc_out=.
	protoc microservices/the_monkeys_users/internal/pb/*.proto --go_out=. --go-grpc_out=.
	protoc microservices/the_monkeys_blog/internal/pb/*.proto --go_out=. --go-grpc_out=.
	protoc microservices/the_monkeys_storage/internal/pb/*.proto --go_out=. --go-grpc_out=.

proto-gen-interservices:
	protoc apis/interservice/**/*.proto --go_out=. --go-grpc_out=.

sql-gen:
	echo "Enter the file's name or description (Node keep it short):"
	@read INPUT_VALUE; \
	migrate create -ext sql -dir schema -seq $$INPUT_VALUE

migrate-up:
	migrate -path schema -database "postgresql://${PSQLUSER}:${PSQLPASS}@${PSQLHOST}:${PSQLPORT}/${PSQLDB}?sslmode=disable" -verbose up

migrate-down:
	migrate -path schema -database "postgresql://${PSQLUSER}:${PSQLPASS}@${PSQLHOST}:${PSQLPORT}/${PSQLDB}?sslmode=disable" -verbose down 1

migrate-force:
	echo "Enter a version:"
	@read INPUT_VALUE; \
	migrate -path schema -database "postgresql://${PSQLUSER}:${PSQLPASS}@${PSQLHOST}:${PSQLPORT}/${PSQLDB}?sslmode=disable" -verbose force $$INPUT_VALUE

proto-gen:
	protoc apis/serviceconn/**/pb/*.proto --go_out=. --go-grpc_out=.
       
    # To Generate Python Recommendation Server code
	python -m grpc_tools.protoc \
    -I=microservices/the_monkeys_ai \
    --python_out=microservices/the_monkeys_ai \
    --grpc_python_out=microservices/the_monkeys_ai \
    microservices/the_monkeys_ai/gw_recom.proto


freeze:
	pip freeze > requirements.txt

# Containerized Go/protobuf toolchain for the social-post domain: no host Go
# or protoc installation is required. Optionally pass
# SOCIAL_POST_CA_CERT=/absolute/path/to/your-ca.crt to trust a local
# TLS-intercepting proxy inside the container; the certificate is mounted
# read-only at build time and is never copied into the repository or image.
social-post-dev:
	docker run --rm -v "$$(pwd):/src" \
		$(if $(SOCIAL_POST_CA_CERT),-v "$(SOCIAL_POST_CA_CERT):/usr/local/share/ca-certificates/extra-ca.crt:ro",) \
		-w /src golang:1.26.1-alpine sh -ec '\
		apk add --no-cache protobuf git ca-certificates; \
		update-ca-certificates; \
		go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.10; \
		go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1; \
		export PATH="$$PATH:/go/bin"; \
		protoc -I . --go_out=. --go-grpc_out=. apis/serviceconn/gateway_social_post/pb/gw_social_post.proto; \
		gofmt -w microservices/the_monkeys_social_post microservices/the_monkeys_gateway/internal/social_post; \
		go test ./microservices/the_monkeys_social_post/... ./microservices/the_monkeys_gateway/internal/social_post/...'