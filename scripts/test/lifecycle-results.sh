# Result collection shared by the Linux lifecycle harness and its child runs.
# Each process appends status updates; the outermost process prints the report.

lifecycle_results_init() {
  local log_dir=$1
  LIFECYCLE_RESULTS_OWNER=false
  if [[ -z "${SPROUT_LIFECYCLE_E2E_RESULTS_FILE:-}" ]]; then
    LIFECYCLE_RESULTS_OWNER=true
    SPROUT_LIFECYCLE_E2E_RESULTS_FILE="$log_dir/results.tsv"
    : > "$SPROUT_LIFECYCLE_E2E_RESULTS_FILE"
    export SPROUT_LIFECYCLE_E2E_RESULTS_FILE
  elif [[ ! -f "$SPROUT_LIFECYCLE_E2E_RESULTS_FILE" || ! -w "$SPROUT_LIFECYCLE_E2E_RESULTS_FILE" ]]; then
    printf 'error: lifecycle results file is unavailable: %s\n' "$SPROUT_LIFECYCLE_E2E_RESULTS_FILE" >&2
    return 1
  fi
}

lifecycle_result() {
  local scenario=$1 test_case=$2 status=$3
  printf '%s\t%s\t%s\n' "$scenario" "$test_case" "$status" >> "$SPROUT_LIFECYCLE_E2E_RESULTS_FILE"
}

lifecycle_results_summary() {
  local exit_status=$1 log_dir=$2
  awk -F '\t' -v exit_status="$exit_status" -v log_dir="$log_dir" '
    {
      key = $1 SUBSEP $2
      if (!(key in status)) {
        order[++total] = key
        scenario[key] = $1
        test_case[key] = $2
      }
      status[key] = $3
    }
    END {
      for (i = 1; i <= total; i++) {
        key = order[i]
        if (status[key] == "PASS") passed++
        else if (status[key] == "FAIL") failed++
        else unfinished++
      }
      success = exit_status == 0 && total > 0 && failed == 0 && unfinished == 0
      printf "\nLinux lifecycle E2E: %s\n\n", success ? "PASSED" : "FAILED"
      printf "  %-10s %-14s %s\n", "RESULT", "SCENARIO", "CASE"
      for (i = 1; i <= total; i++) {
        key = order[i]
        printf "  %-10s %-14s %s\n", status[key], scenario[key], test_case[key]
      }
      printf "\n%d passed, %d failed, %d not finished\n", passed, failed, unfinished
      if (exit_status != 0) printf "Harness exited with status %d; see preceding output for the error.\n", exit_status
      printf "Logs: %s\n", log_dir
      exit !success
    }
  ' "$SPROUT_LIFECYCLE_E2E_RESULTS_FILE"
}
