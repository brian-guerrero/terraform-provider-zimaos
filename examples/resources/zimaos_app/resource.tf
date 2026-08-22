resource "zimaos_app" "uptime_kuma" {
  name         = "uptime-kuma"
  compose_yaml = file("${path.module}/compose/uptime-kuma.yaml")

  # Validate the compose spec against the device during plan (default: true).
  dry_run_on_plan = true

  # Passed through to the API's own port-conflict check (default: true).
  check_port_conflict = true

  # Keep the app's config folder on destroy instead of deleting it.
  # Defaults to false (the API's own default is the opposite: it deletes the
  # config folder unless told otherwise) so destroying a resource never
  # silently drops data you didn't explicitly opt out of keeping.
  retain_config_on_destroy = false

  # start / stop / restart — drives the app's running state. Defaults to "start".
  desired_state = "start"
}
