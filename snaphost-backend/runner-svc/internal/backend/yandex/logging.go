package yandex

import (
	"strings"

	loggingpb "github.com/yandex-cloud/go-genproto/yandex/cloud/logging/v1"
	containerspb "github.com/yandex-cloud/go-genproto/yandex/cloud/serverless/containers/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// logLevels maps the configured level name onto Cloud Logging's enum. Only
// names Yandex actually accepts are listed; anything else is treated as
// unset rather than guessed at, because a wrong minimum level silently drops
// exactly the output someone is trying to read.
var logLevels = map[string]loggingpb.LogLevel_Level{
	"TRACE": loggingpb.LogLevel_TRACE,
	"DEBUG": loggingpb.LogLevel_DEBUG,
	"INFO":  loggingpb.LogLevel_INFO,
	"WARN":  loggingpb.LogLevel_WARN,
	"ERROR": loggingpb.LogLevel_ERROR,
	"FATAL": loggingpb.LogLevel_FATAL,
}

// revisionLogOptions decides where a user container's stdout/stderr goes.
//
// This is the Task 13a collection point. Before it existed, runner-svc created
// revisions with no log options at all, so a container that crashed on startup
// left no evidence anywhere and diagnosis meant pulling the image onto the VDS
// and running it by hand — the 2026-07-19 incident.
//
// Returning nil means "send no log_options at all", which is not the same as
// disabling collection: Yandex then applies its own default and writes to the
// folder's default log group. Naming the folder explicitly would achieve the
// same thing while adding a permission check that can fail, so the unconfigured
// case deliberately says nothing.
//
// The output is operator-only. It is deliberately not published to the Redis
// channels the deploy owner's WebSocket subscribes to: runtime logs are noisy
// and can carry infrastructure detail, so users get status plus a
// recommendation instead (Task 13b).
func revisionLogOptions(logGroupID, minLevel string, disabled bool) *containerspb.LogOptions {
	if disabled {
		return &containerspb.LogOptions{Disabled: true}
	}
	if logGroupID == "" {
		return nil
	}

	opts := &containerspb.LogOptions{
		Destination: &containerspb.LogOptions_LogGroupId{LogGroupId: logGroupID},
	}
	if level, ok := logLevels[minLevel]; ok {
		opts.MinLevel = level
	}
	return opts
}

// isLogOptionsRejection reports whether a DeployRevision error is Yandex
// refusing the log configuration rather than refusing the deployment.
//
// This distinction is load-bearing. On 2026-08-04 a freshly created log group
// produced "PermissionDenied: Not enough permissions to use log group <id>" on
// every revision, and because the runner treated it like any other deploy
// failure, a logging misconfiguration became a total outage for new deploys —
// collecting logs is worth less than being able to deploy at all.
func isLogOptionsRejection(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.PermissionDenied, codes.NotFound, codes.InvalidArgument:
	default:
		return false
	}
	return strings.Contains(strings.ToLower(st.Message()), "log group")
}
