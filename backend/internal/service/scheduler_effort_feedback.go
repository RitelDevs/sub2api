package service

func (s *defaultOpenAIAccountScheduler) shouldEscapeStickyForRequest(req OpenAIAccountScheduleRequest, id int64, cfg openAIStickyEscapeConfig) (string, float64, float64, bool) {
	if s.service != nil {
		priority := s.service.prioritySchedulingRuntimeConfig()
		if req.Platform == PlatformOpenAI && req.UseUpstreamTokenCost && req.RequiredImageCapability == "" && priorityApplies(priority, req) {
			if !cfg.enabled || priorityLatencyOwnershipBound(req) || req.DisableStickyEscape || req.EncryptedReasoning {
				return "", 0, 0, false
			}
			evidence := s.priorityErrorEvidence(req, priority, id, s.stats.currentTime())
			if priority.ErrorRateWeight > 0 && evidence.ErrorRate != nil && evidence.SmoothedRate > cfg.errorRate {
				return "error_rate", evidence.SmoothedRate, 0, true
			}
			// TTFT admission/escape uses the same request-scoped history as
			// priority scoring; account-wide response totals cannot replace it.
			return "", 0, 0, false
		}
	}
	if !req.ObservationEffortScoped {
		return s.shouldEscapeStickyAccount(id, cfg)
	}
	if !cfg.enabled || s.stats == nil {
		return "", 0, 0, false
	}
	e := s.stats.attempts.snapshot(priorityRequestAttemptKey(req, id), s.stats.currentTime(), 10, 5)
	if e.LatencySamples >= 5 {
		ttft := e.LatencyTotalMS / float64(e.LatencySamples)
		if ttft > cfg.ttftMs {
			return "ttft", e.SmoothedRate, ttft, true
		}
	}
	if e.Successes+e.Failures >= 5 && e.SmoothedRate > cfg.errorRate {
		return "error_rate", e.SmoothedRate, 0, true
	}
	return "", 0, 0, false
}
