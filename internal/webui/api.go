package webui

import (
	"net/http"

	"time"

	"github.com/Zhengmingming1/tidebot/internal/buildinfo"
)

// Version is shown in the UI footer.
var Version = buildinfo.Version

func (s *Server) registerAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/capabilities/dependency-preferences", s.withAuthCSRF(s.handleCapabilityDependencyPreferences))
	mux.HandleFunc("/api/capability-secrets", s.withAuthCSRF(s.handleCapabilitySecrets))
	mux.HandleFunc("/api/capability-grants", s.withAuthCSRF(s.handleCapabilityGrants))
	mux.HandleFunc("/api/capability-bindings", s.withAuthCSRF(s.handleCapabilityBindings))
	mux.HandleFunc("/api/capability-bindings/", s.withAuthCSRF(s.handleCapabilityBindings))
	mux.HandleFunc("/api/skill-repositories", s.withAuthCSRF(s.handleSkillRepositories))
	mux.HandleFunc("/api/skill-repositories/", s.withAuthCSRF(s.handleSkillRepositorySub))
	mux.HandleFunc("/api/dependencies/preflight", s.withAuthCSRF(s.handleDependencyPreflight))
	mux.HandleFunc("/api/skill-sets/preview", s.withAuthCSRF(s.handleSkillSetPreview))
	mux.HandleFunc("/api/skill-manifests/", s.withAuthCSRF(s.handleSkillManifest))
	mux.HandleFunc("/api/workflows", s.withAuthCSRF(s.handleWorkflows))
	mux.HandleFunc("/api/workflows/", s.withAuthCSRF(s.handleWorkflowSub))
	mux.HandleFunc("/api/workflow-runs", s.withAuthCSRF(s.handleWorkflowRuns))
	mux.HandleFunc("/api/workflow-runs/", s.withAuthCSRF(s.handleWorkflowRunSub))
	mux.HandleFunc("/api/dependencies/check", s.withAuthCSRF(s.handleDependencyCheck))
	mux.HandleFunc("/api/dependencies/check/", s.withAuthCSRF(s.handleDependencyCheckStatus))
	mux.HandleFunc("/api/capabilities", s.withAuthCSRF(s.handleCapabilities))
	mux.HandleFunc("/api/capabilities/", s.handleCapabilityRoute)
	mux.HandleFunc("/api/chat-sessions", s.withAuthCSRF(s.handleChatSessions))
	mux.HandleFunc("/api/chat-sessions/", s.withAuthCSRF(s.handleChatSessionSub))
	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/logout", s.withAuthCSRF(s.handleLogout))
	mux.HandleFunc("/api/me", s.withAuthCSRF(s.handleMe))
	mux.HandleFunc("/api/overview", s.withAuthCSRF(s.handleOverview))
	mux.HandleFunc("/api/rev", s.withAuthCSRF(s.handleRev))
	mux.HandleFunc("/api/events", s.withAuthCSRF(s.handleEvents))
	mux.HandleFunc("/api/tasks", s.withAuthCSRF(s.handleTasks))
	mux.HandleFunc("/api/tasks/", s.withAuthCSRF(s.handleTaskSub))
	mux.HandleFunc("/api/amendments/", s.withAuthCSRF(s.handleAmendAction))
	mux.HandleFunc("/api/skill-set-usage", s.withAuthCSRF(s.handleSkillSetUsage))
	mux.HandleFunc("/api/skill-set-usage/apply", s.withAuthCSRF(s.handleSkillSetUsageApply))
	mux.HandleFunc("/api/skill-set-evals", s.withAuthCSRF(s.handleSkillSetEvals))
	mux.HandleFunc("/api/skill-set-evals/", s.withAuthCSRF(s.handleSkillSetEvalSub))
	mux.HandleFunc("/api/skill-sets", s.withAuthCSRF(s.handleSkillSets))
	mux.HandleFunc("/api/skill-sets/import", s.withAuthCSRF(s.handleSkillSetImport))
	mux.HandleFunc("/api/skill-sets/", s.withAuthCSRF(s.handleSkillSetSub))
	mux.HandleFunc("/api/skills", s.withAuthCSRF(s.handleSkills))
	mux.HandleFunc("/api/skills/import", s.withAuthCSRF(s.handleSkillsImport))
	mux.HandleFunc("/api/skills/upload", s.withAuthCSRF(s.handleSkillUpload))
	mux.HandleFunc("/api/providers", s.withAuthCSRF(s.handleProviders))
	mux.HandleFunc("/api/providers/infer", s.withAuthCSRF(postOnly(s.handleProviderInfer)))
	mux.HandleFunc("/api/plugins/prefill", s.withAuthCSRF(postOnly(s.handlePluginPrefill)))
	mux.HandleFunc("/api/tasks/workspace-check", s.withAuthCSRF(s.handleWorkspaceCheck))
	mux.HandleFunc("/api/tasks/infer", s.withAuthCSRF(postOnly(s.handleTaskInfer)))
	mux.HandleFunc("/api/plugins/upload", s.withAuthCSRF(s.handlePluginUpload))
	mux.HandleFunc("/api/providers/", s.withAuthCSRF(s.handleProviderSub))
	mux.HandleFunc("/api/skills/", s.withAuthCSRF(s.handleSkillSub))
	mux.HandleFunc("/api/runs", s.withAuthCSRF(s.handleRuns))
	mux.HandleFunc("/api/runs/quick", s.withAuthCSRF(s.handleQuickRun))
	mux.HandleFunc("/api/runs/defaults", s.withAuthCSRF(s.handleRunDefaults))
	mux.HandleFunc("/api/runs/recent-prompts", s.withAuthCSRF(s.handleRecentPrompts))
	mux.HandleFunc("/api/runs/counts", s.withAuthCSRF(s.handleRunCounts))
	mux.HandleFunc("/api/runs/export", s.withAuthCSRF(s.handleRunsExport))
	mux.HandleFunc("/api/runs/", s.withAuthCSRF(s.handleRunSub))
	mux.HandleFunc("/api/buttons", s.withAuthCSRF(s.handleButtons))
	mux.HandleFunc("/api/buttons/", s.withAuthCSRF(s.handleButtonRun))
	mux.HandleFunc("/api/schedule", s.withAuthCSRF(s.handleSchedule))
	mux.HandleFunc("/api/dashboard", s.withAuthCSRF(s.handleDashboard))
	mux.HandleFunc("/api/scheduler/preview", s.withAuthCSRF(s.handleCronPreview))
	mux.HandleFunc("/api/schedule/preview", s.withAuthCSRF(s.handleCronPreview))
	mux.HandleFunc("/api/spend", s.withAuthCSRF(s.handleSpend))
	mux.HandleFunc("/api/spend/breakdown", s.withAuthCSRF(s.handleSpendBreakdown))
	mux.HandleFunc("/api/spend/export", s.withAuthCSRF(s.handleSpendExport))
	mux.HandleFunc("/api/spend/unlimited-notice", s.withAuthCSRF(s.handleUnlimitedNotice))
	mux.HandleFunc("/api/claude/status", s.withAuthCSRF(s.handleClaudeStatus))
	mux.HandleFunc("/api/desktop/pick-file", s.withAuthCSRF(s.handlePickFile))
	mux.HandleFunc("/api/desktop/pick-folder", s.withAuthCSRF(s.handlePickFolder))
	mux.HandleFunc("/api/desktop/save", s.withAuthCSRF(s.handleDesktopSave))
	mux.HandleFunc("/api/desktop/open", s.withAuthCSRF(s.handleDesktopOpen))
	mux.HandleFunc("/api/notifications/center", s.withAuthCSRF(s.handleNotifCenter))
	mux.HandleFunc("/api/notifications/clear", s.withAuthCSRF(s.handleNotifClear))
	mux.HandleFunc("/api/notifications/read", s.withAuthCSRF(s.handleNotifRead))
	mux.HandleFunc("/api/auth/2fa/", s.withAuthCSRF(s.handle2FA))
	mux.HandleFunc("/api/auth/password", s.withAuthCSRF(s.handlePassword))
	mux.HandleFunc("/api/auth/sessions/others", s.withAuthCSRF(s.handleSessionsOthers))
	mux.HandleFunc("/api/auth/sessions", s.withAuthCSRF(s.handleSessions))
	mux.HandleFunc("/api/auth/sessions/signout", s.withAuthCSRF(s.handleSessionSignOut))
	mux.HandleFunc("/api/notifications", s.withAuthCSRF(s.handleNotifications))
	mux.HandleFunc("/api/search", s.withAuthCSRF(s.handleSearch))
	mux.HandleFunc("/api/settings", s.withAuthCSRF(s.handleSettings))
	mux.HandleFunc("/api/webui", s.withAuthCSRF(s.handleWebUIToggle))
	mux.HandleFunc("/api/budgets", s.withAuthCSRF(s.handleBudgets))
	mux.HandleFunc("/api/settings/language", s.withAuthCSRF(s.handleLanguage))
	mux.HandleFunc("/api/settings/appearance", s.withAuthCSRF(s.handleAppearance))
	mux.HandleFunc("/api/rpc/", s.withAuthCSRF(s.handleExt))
}

func (s *Server) loc() *time.Location {
	if s.deps.LocFn != nil {
		if l := s.deps.LocFn(); l != nil {
			return l
		}
	}
	if s.deps.Location != nil {
		return s.deps.Location
	}
	return time.Local
}
