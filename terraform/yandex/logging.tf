# Dedicated log group for user container stdout/stderr (Task 13a).
#
# Without this, a deployed user application's output goes nowhere: runner-svc
# created revisions with no log options, so diagnosing a crashed deploy meant
# pulling the image onto the VDS and running it by hand (2026-07-19 incident).
#
# A dedicated group rather than the folder's default one, for two reasons:
# retention is set per group, and this stream is untrusted user output that
# should not be interleaved with the control plane's own logging.
resource "yandex_logging_group" "runtime" {
  name        = var.runtime_log_group_name
  folder_id   = var.folder_id
  description = "stdout/stderr of user Serverless Containers. Operator-only; never exposed to deploy owners."

  # User application output, kept long enough to diagnose a report that arrives
  # the next working day and no longer. It is the noisiest data we hold and the
  # least useful once the deploy is gone.
  retention_period = var.runtime_log_retention
}
