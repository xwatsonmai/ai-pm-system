.PHONY: setup run build test

setup:
	npm ci --prefix runtime --ignore-scripts
	go mod download

run:
	go run ./cmd/server

build:
	go build -o bin/ai-pm ./cmd/server

test:
	python3 -m unittest discover -s deploy -p '*_test.py'
	go test -race -timeout 60s ./...
	go vet ./...
	npm test --prefix runtime
	node --test internal/web/*.test.mjs
	node --check internal/web/static/app.js
	node --check internal/web/static/requirement-editor.mjs
	node --check internal/web/static/requirement-review.mjs
	node --check internal/web/static/requirement-review-state.mjs
	node --check internal/web/static/requirement-intake.mjs
	node --check internal/web/static/project-versions.mjs
	node --check internal/web/static/project-timeline.mjs
	node --check internal/web/static/requirement-iterations.mjs
	node --check internal/web/static/todo.mjs
