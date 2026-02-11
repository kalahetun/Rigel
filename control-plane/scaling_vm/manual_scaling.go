package scaling_vm

import (
	"log/slog"
)

//init | scale_up | deploy_proxy | deploy_plane | attach_envoy | sleep | start | release

func (s *Scaler) ManualScaling(pre, action, ip, vmName string) {

	// 尝试获取锁，若获取不到则直接返回
	if !s.tryMu.TryLock() {
		s.logger.Warn("cannot get lock", slog.String("pre", pre),
			slog.Any("err", "cannot get lock"),
		)
		return
	}
	defer s.tryMu.Unlock()

	//---------------------------------------------------------------
	vm_ := VM{}
	if ip != "" && vmName != "" {
		s.logger.Info("ManualScaling", slog.String("pre", pre),
			slog.String("ip", ip), slog.String("vmName", vmName))
		vm_.VMName = vmName
		vm_.PublicIP = ip
	}

	switch action {
	case "scale_up":
		ok, vm := s.triggerScaling1_(1, vm_, pre, s.logger)
		if vm.PublicIP == "" {
			s.logger.Error("create vm failed", slog.String("pre", pre))
		} else if vm.PublicIP != "" && !ok {
			s.logger.Warn("create vm success but deploy failed",
				slog.String("pre", pre), slog.String("vm", vm.PublicIP))
		} else if vm.PublicIP != "" && ok {
			s.logger.Info("triggerScaling1_ success", slog.String("pre", pre))
		}

	case "sleep":
		s.triggerDormant(vm_, pre)

	case "start":
		s.triggerScaling2(vm_, pre)

	case "release":
		s.triggerRelease(vm_, pre)

	default:
		s.logger.Warn("the action is nonexist", slog.String("pre", pre), slog.String("odd action", action))

	}

	//-----------------------------------------------------------------------------

	s.ScalerDump(pre, nil)
	s.logger.Info("ManualScaling state change", slog.String("pre", pre),
		slog.String("old state", s.ManualAction), slog.String("new state", action))
	s.ManualAction = action
}
