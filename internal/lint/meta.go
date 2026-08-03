package lint

// Meta describes a rule for machine-readable outputs (SARIF, docs).
type Meta struct {
	ID    string
	Name  string
	Short string
}

// RuleMeta indexes rule metadata by ID.
var RuleMeta = map[string]Meta{
	"D001":  {"D001", "MissingConcurrencyCancellation", "PR-triggered workflow lacks a concurrency group with cancel-in-progress, so superseded runs keep burning minutes"},
	"D002":  {"D002", "NoJobTimeout", "job has no timeout-minutes; a hung job bills the 6-hour default"},
	"D003":  {"D003", "UncachedSetupAction", "setup-node/python/java without built-in dependency caching re-downloads packages every run"},
	"D004":  {"D004", "FullFetchDepth", "checkout with fetch-depth: 0 clones full history on every run"},
	"D005":  {"D005", "HighFrequencyCron", "schedule runs very frequently; GitHub delays or drops high-frequency crons"},
	"D006":  {"D006", "ExpensiveRunnerOnEveryPush", "macOS/Windows/large runner triggered on every push or PR (macOS bills 10x, Windows 2x)"},
	"D007":  {"D007", "DockerBuildWithoutLayerCache", "docker build-push without cache-from/cache-to rebuilds all layers every run"},
	"D008":  {"D008", "CacheWithoutRestoreKeys", "actions/cache without restore-keys misses on every lockfile change"},
	"D009":  {"D009", "ContinueOnErrorMasksFailures", "continue-on-error hides real failures and wastes debugging time"},
	"D010":  {"D010", "DefaultArtifactRetention", "artifacts keep the 90-day default retention and count against storage"},
	"D011":  {"D011", "LargeMatrixOnPRs", "large matrix runs in full on every PR"},
	"D012":  {"D012", "NpmInstallInCI", "npm install in CI is slower and less reproducible than npm ci"},
	"D013":  {"D013", "PushAndPullRequestDoubleRun", "workflow triggers on both unscoped push and pull_request, running the same commit twice for every PR"},
	"D014":  {"D014", "TopOfHourCron", "cron fires at minute 0, the peak-load window where GitHub delays or drops scheduled runs"},
	"D015":  {"D015", "RetiredActionVersion", "step uses an action version GitHub has shut down (artifact v1-v3, cache v1-v2); it fails at runtime"},
	"D016":  {"D016", "RetiredRunnerLabel", "job requests a hosted runner label GitHub has retired; the job cannot run"},
	"D017":  {"D017", "NoActionsUpdateAutomation", "nothing updates the repo's action pins (no dependabot github-actions ecosystem, no renovate); they rot until they hit shut-down versions"},
	"D018":  {"D018", "DeprecatedWorkflowCommand", "run step emits a stdout workflow command GitHub has deprecated or disabled (set-env/add-path error at runtime; set-output/save-state warn on every run, removal announced)"},
	"D020":  {"D020", "DeprecatingRunnerLabel", "job requests a hosted runner label with an announced retirement; brownouts start on the deprecation date and the job stops running on the removal date"},
	"D019":  {"D019", "DeprecatedActionRuntime", "action.yml declares runs.using node12/node16 (runtimes removed from runners) or node20 (deprecated; removal from runners announced for fall 2026) — declare node24"},
	"D021":  {"D021", "UnguardedCron", "scheduled workflow has no github.repository guard: once a fork owner enables Actions, the cron runs in every fork — failing on missing secrets or spamming bot actions"},
	"parse": {"parse", "UnparseableWorkflow", "workflow file could not be parsed as YAML"},
}
