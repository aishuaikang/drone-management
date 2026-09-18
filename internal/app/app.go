// Package app wires the network edition backend.
package app

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"drone-management/internal/config"
	"drone-management/internal/counterstrike"
	"drone-management/internal/fpv"
	"drone-management/internal/fpvrecord"
	"drone-management/internal/fpvvideo"
	"drone-management/internal/httpapi"
	"drone-management/internal/interference"
	"drone-management/internal/interferencereport"
	"drone-management/internal/intrusion"
	"drone-management/internal/license"
	"drone-management/internal/lingyun"
	"drone-management/internal/model"
	networkmanager "drone-management/internal/network"
	"drone-management/internal/offlinemap"
	"drone-management/internal/position"
	"drone-management/internal/protocol"
	"drone-management/internal/settings"
	"drone-management/internal/store"
)

// App owns all long-running backend services.
type App struct {
	httpServer          *httpapi.Server
	state               *store.Store
	intrusions          *intrusion.Store
	fpvRecords          *fpvrecord.Store
	interferenceReports *interferencereport.Store
	interference        *interference.Service
	cancel              context.CancelFunc
	done                chan struct{}
}

// New builds a runnable application.
func New(cfg config.Config) (*App, error) {
	state := store.New(cfg.MaxPositionTargets, cfg.MaxFPVTargets)
	state.SetPositionTTL(cfg.PositionTargetTTL)
	if point, updatedAt, ok, err := settings.LoadManualDeviceLocation(cfg.ManualLocationPath); err != nil {
		slog.Warn("读取手动设备定位失败", "path", cfg.ManualLocationPath, "error", err)
	} else if ok {
		state.LoadManualDeviceLocation(point, updatedAt)
	}
	userSettings := settings.NewUserStore(cfg.UserSettingsPath)
	loadedUserSettings := model.UserSettingsWithDefaults(model.UserSettings{})
	if loaded, ok, err := userSettings.LoadUser(); err != nil {
		slog.Warn("读取用户设置失败", "path", cfg.UserSettingsPath, "error", err)
	} else if ok {
		loaded = model.UserSettingsWithDefaults(loaded)
		loadedUserSettings = loaded
		seconds := model.UserSettingsPositionExpireSeconds(loaded)
		state.SetPositionTTL(time.Duration(seconds) * time.Second)
		cfg = configWithUserTCPPorts(cfg, loaded)
		cfg = configWithUserFPVVideoSettings(cfg, loaded)
	}
	intrusionStore, err := intrusion.NewStore(cfg.IntrusionDBPath)
	if err != nil {
		return nil, err
	}
	intrusionStore.SetDeviceLocationProvider(state.DeviceLocation)
	state.SetPositionArchiver(intrusionStore)
	state.SetFPVArchiver(intrusionStore)
	fpvRecordStore, err := fpvrecord.NewStore(cfg.FPVVideoRecordDBPath)
	if err != nil {
		_ = intrusionStore.Close()
		return nil, err
	}
	interferenceReportStore, err := interferencereport.NewStore(cfg.InterferenceReportDBPath)
	if err != nil {
		_ = fpvRecordStore.Close()
		_ = intrusionStore.Close()
		return nil, err
	}
	if _, err := interferenceReportStore.CloseRunning("service_restarted", time.Now()); err != nil {
		slog.Warn("闭合运行中干扰报告失败", "error", err)
	}
	interferenceSvc := newInterferenceService(cfg, state)
	interferenceSvc.SetReportStore(interferenceReportStore)
	interferenceSvc.SetUserSettingsStore(userSettings)
	if loadedUserSettings.ScreenStrikeUnattended != nil && loadedUserSettings.ScreenStrikeUnattended.Enabled {
		if _, err := interferenceSvc.RestoreUnattended(*loadedUserSettings.ScreenStrikeUnattended); err != nil {
			slog.Warn("恢复无人值守干扰失败", "error", err)
		}
	}
	offlineMapSvc := offlinemap.NewService(cfg.OfflineMapPath)
	networkSvc := networkmanager.NewService()
	licenseSvc := license.NewService(cfg.LicensePath, func() (string, error) {
		return model.NewLingyunDeviceSN(), nil
	})

	positionSvc := position.NewService(state, position.Options{
		Host:              cfg.TCPBindHost,
		Port:              cfg.PositionTCPPort,
		UDPEnabled:        cfg.PositionUDPEnabled,
		UDPPort:           cfg.PositionUDPPort,
		BindRetryInterval: cfg.TCPBindRetry,
		ReadIdleTimeout:   cfg.TCPReadIdleTimeout,
		O3Decrypt: position.O3DecryptOptions{
			Enabled:        cfg.O3Decrypt.Enabled,
			Broker:         cfg.O3Decrypt.Broker,
			Port:           cfg.O3Decrypt.Port,
			Username:       cfg.O3Decrypt.Username,
			Password:       cfg.O3Decrypt.Password,
			Timeout:        cfg.O3Decrypt.Timeout,
			ConnectTimeout: cfg.O3Decrypt.ConnectTimeout,
		},
	})
	fpvSvc := fpv.NewService(state, fpv.Options{
		Host:              cfg.TCPBindHost,
		Port:              cfg.FPVTCPPort,
		BindRetryInterval: cfg.TCPBindRetry,
		ReadIdleTimeout:   cfg.TCPReadIdleTimeout,
		CommandTimeout:    cfg.FPVCommandTimeout,
	})
	lingyunSvc := lingyun.NewService(state, loadedUserSettings, lingyun.WithInterferenceController(interferenceSvc))
	counterStrikeSvc := counterstrike.NewService(state, loadedUserSettings, counterstrike.WithInterferenceController(interferenceSvc))
	protocolManager := protocol.NewManager(lingyunSvc, counterStrikeSvc)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		wg.Add(4)
		go func() {
			defer wg.Done()
			positionSvc.Run(ctx)
		}()
		go func() {
			defer wg.Done()
			fpvSvc.Run(ctx)
		}()
		go func() {
			defer wg.Done()
			protocolManager.Run(ctx)
		}()
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case now := <-ticker.C:
					if err := state.SweepFPV(ctx, now); err != nil {
						slog.Warn("归档 FPV 入侵目标失败", "error", err)
					}
				}
			}
		}()
		<-ctx.Done()
		wg.Wait()
	}()

	return &App{
		httpServer: httpapi.New(
			cfg,
			state,
			positionSvc,
			fpvSvc,
			httpapi.WithUserSettingsStore(userSettings),
			httpapi.WithLingyunService(lingyunSvc),
			httpapi.WithCounterStrikeService(counterStrikeSvc),
			httpapi.WithIntrusionStore(intrusionStore),
			httpapi.WithFPVVideoRecordStore(fpvRecordStore),
			httpapi.WithInterferenceService(interferenceSvc),
			httpapi.WithInterferenceReportStore(interferenceReportStore),
			httpapi.WithOfflineMapService(offlineMapSvc),
			httpapi.WithNetworkService(networkSvc),
			httpapi.WithLicenseService(licenseSvc),
		),
		state:               state,
		intrusions:          intrusionStore,
		fpvRecords:          fpvRecordStore,
		interferenceReports: interferenceReportStore,
		interference:        interferenceSvc,
		cancel:              cancel,
		done:                done,
	}, nil
}

func configWithUserFPVVideoSettings(cfg config.Config, userSettings model.UserSettings) config.Config {
	rtmpURL := strings.TrimSpace(userSettings.FPVVideoRTMPURL)
	if err := fpvvideo.ValidateRTMPSettings(userSettings.FPVVideoRTMPEnabled, rtmpURL); err != nil {
		slog.Warn("ignoring invalid saved FPV RTMP settings", "error", err)
		cfg.FPVVideo.RTMPEnabled = false
		cfg.FPVVideo.RTMPURL = ""
	} else {
		cfg.FPVVideo.RTMPEnabled = userSettings.FPVVideoRTMPEnabled
		cfg.FPVVideo.RTMPURL = rtmpURL
	}
	host := strings.TrimSpace(userSettings.FPVVideoWebRTCHost)
	if host == "" {
		return cfg
	}
	addresses, err := fpvvideo.NetworkAddresses()
	if err != nil {
		slog.Warn("failed to list local addresses; ignoring saved WebRTC host", "host", host, "error", err)
		return cfg
	}
	for _, address := range addresses {
		if address.Address == host {
			cfg.FPVVideo.WebRTCListenHost = host
			return cfg
		}
	}
	slog.Warn("saved WebRTC host is not assigned to an active local interface", "host", host)
	return cfg
}

func newInterferenceService(cfg config.Config, state *store.Store) *interference.Service {
	relay := interference.NewRelayController(interference.RelayOptions{
		Host:    cfg.InterferenceRelay.Host,
		Port:    cfg.InterferenceRelay.Port,
		Address: cfg.InterferenceRelay.Address,
		Timeout: cfg.InterferenceRelay.Timeout,
	})
	service := interference.NewService(
		state,
		interference.DefaultChannels(),
		relay.Output,
	)
	service.SetConnectionStatusProvider(relay.Status)
	return service
}

func configWithUserTCPPorts(cfg config.Config, userSettings model.UserSettings) config.Config {
	positionPort := cfg.PositionTCPPort
	fpvPort := cfg.FPVTCPPort
	if userSettings.PositionTCPPort != nil {
		positionPort = *userSettings.PositionTCPPort
	}
	if userSettings.FPVTCPPort != nil {
		fpvPort = *userSettings.FPVTCPPort
	}
	if positionPort < model.MinTCPPort ||
		positionPort > model.MaxTCPPort ||
		fpvPort < model.MinTCPPort ||
		fpvPort > model.MaxTCPPort ||
		positionPort == fpvPort {
		slog.Warn("忽略无效的用户 TCP 端口设置", "positionPort", positionPort, "fpvPort", fpvPort)
		return cfg
	}
	cfg.PositionTCPPort = positionPort
	cfg.FPVTCPPort = fpvPort
	return cfg
}

// ListenAndServe starts the HTTP API.
func (a *App) ListenAndServe() error {
	return a.httpServer.ListenAndServe()
}

// Shutdown stops all services.
func (a *App) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := a.httpServer.Shutdown(ctx)

	if a.cancel != nil {
		a.cancel()
	}
	if a.done != nil {
		<-a.done
	}
	if a.interference != nil {
		a.interference.Shutdown()
	}
	if a.state != nil {
		flushCtx, flushCancel := context.WithTimeout(context.Background(), 15*time.Second)
		flushErr := a.state.DrainFPV(flushCtx)
		flushCancel()
		err = errors.Join(err, flushErr)
	}
	if a.intrusions != nil {
		err = errors.Join(err, a.intrusions.Close())
	}
	if a.fpvRecords != nil {
		err = errors.Join(err, a.fpvRecords.Close())
	}
	if a.interferenceReports != nil {
		err = errors.Join(err, a.interferenceReports.Close())
	}
	return err
}
