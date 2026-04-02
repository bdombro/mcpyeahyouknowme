package notebook

// CLI handler tests are excluded from coverage per project convention.
// The RunAdd/RunRemove/RunList/RunReset functions call os.Exit so they
// are not tested directly; coverage is achieved through integration tests
// in src/main_test.go which dispatch the CLI path end-to-end.
