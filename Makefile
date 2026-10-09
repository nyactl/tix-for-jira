.PHONY: test lint dev it

# Unit tests with the race detector.
test:
	go test -race ./...

lint:
	golangci-lint run ./...
	golangci-lint run --build-tags integration ./integration/

# Development build next to the released binary; it always needs --profile
# or TIX_JIRA_PROFILE, so it cannot touch production by accident.
dev:
	go build -o "$$(go env GOPATH)/bin/tix-dev" ./cmd/tix

# Integration tests against the test site; see integration/README.md.
# Set TIX_JIRA_IT_PROJECT, TIX_JIRA_IT_OTHER and optionally TIX_JIRA_IT_OTHER_PROFILE.
it:
	TIX_JIRA_PROFILE=$${TIX_JIRA_PROFILE:-test} go test -tags integration -count=1 -v ./integration/
