package scaling_vm

import (
	"log/slog"
	"time"
)

//init | scale_up | deploy_proxy | deploy_plane | attach_envoy | sleep | start | release

func (s *Scaler) ManualScaling(pre, action string) {

	// 尝试获取锁，若获取不到则直接返回
	if !s.tryLock(1 * time.Second) {
		s.logger.Warn("cannot get lock", slog.String("pre", pre),
			slog.Any("err", "cannot get lock"),
		)
		return
	}
	defer s.mu.Unlock()

	//---------------------------------------------------------------

	node := s.Node
	switch action {
	case "scale_up":
		if ok, vm := s.triggerScaling1(1, pre, s.logger); ok {
			node.ScaledVMs = append(node.ScaledVMs, vm)
		} else {
			s.logger.Error("triggerScaling1 failed", slog.String("pre", pre))
		}

	case "sleep":
		s.triggerDormant(pre)

	case "start":
		s.triggerScaling2(pre)

	case "release":
		s.triggerRelease(pre)

	default:
		s.logger.Warn("the action is nonexist", slog.String("pre", pre), slog.String("odd action", action))

	}

	//-----------------------------------------------------------------------------

	s.ScalerDump(pre, nil)
	s.logger.Info("ManualScaling state change", slog.String("pre", pre),
		slog.String("old state", s.ManualAction), slog.String("new state", action))
	s.ManualAction = action
}
