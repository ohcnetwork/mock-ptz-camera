package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	log "github.com/sirupsen/logrus"

	"github.com/ohcnetwork/mock-ptz-camera/auth"
	"github.com/ohcnetwork/mock-ptz-camera/config"
	"github.com/ohcnetwork/mock-ptz-camera/media"
	"github.com/ohcnetwork/mock-ptz-camera/netutil"
	"github.com/ohcnetwork/mock-ptz-camera/onvif"
	"github.com/ohcnetwork/mock-ptz-camera/ptz"
	"github.com/ohcnetwork/mock-ptz-camera/renderer"
	"github.com/ohcnetwork/mock-ptz-camera/rtsp"
	"github.com/ohcnetwork/mock-ptz-camera/web"
)

func main() {
	cfg := config.Load()

	level, err := log.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = log.InfoLevel
	}
	log.SetLevel(level)
	log.SetFormatter(&log.TextFormatter{FullTimestamp: true})

	creds := auth.Credentials{
		Username: cfg.Username,
		Password: cfg.Password,
	}

	log.Info("starting mock PTZ camera")
	log.WithFields(log.Fields{
		"width": cfg.Width, "height": cfg.Height, "fps": cfg.FPS,
	}).Info("resolution configured")

	hostIP := cfg.HostIP
	if hostIP == "" {
		hostIP = netutil.DetectHostIP()
		log.WithField("ip", hostIP).Info("detected host IP")
	} else {
		log.WithField("ip", hostIP).Info("using HOST_IP override")
	}

	httpScheme := "http"
	rtspScheme := "rtsp"
	if cfg.TLSEnabled {
		httpScheme = "https"
		rtspScheme = "rtsps"
	}

	eventsService := onvif.NewEventsService(
		fmt.Sprintf("%s://%s:%d/onvif/subscription", httpScheme, hostIP, cfg.WebPort),
	)

	ptzState := ptz.NewState(func(status ptz.Status) {
		eventsService.OnPTZPositionChanged(status)
	})

	var activeRenderer renderer.Renderer
	switch cfg.Renderer {
	case "pano":
		panoRenderer, err := renderer.NewPanoRenderer(cfg.Width, cfg.Height, cfg.PanoImage)
		if err != nil {
			log.WithError(err).Fatal("failed to create pano renderer")
		}
		activeRenderer = panoRenderer
		log.WithField("image", cfg.PanoImage).Info("using pano renderer")
	default:
		activeRenderer = renderer.NewTestRenderer(cfg.Width, cfg.Height)
		log.Info("using test pattern renderer")
	}

	sps, pps, err := renderer.BootstrapSPSPPS(cfg.Width, cfg.Height, cfg.FPS, cfg.Bitrate)
	if err != nil {
		log.WithError(err).Fatal("failed to bootstrap SPS/PPS")
	}

	var tlsCfg *tls.Config
	if cfg.TLSEnabled {
		tlsCfg, err = netutil.SetupTLS(netutil.TLSOptions{
			CertFile: cfg.TLSCertFile,
			KeyFile:  cfg.TLSKeyFile,
			CertDir:  cfg.TLSCertDir,
			HostIP:   hostIP,
		})
		if err != nil {
			log.WithError(err).Fatal("failed to set up TLS")
		}
	}

	rtspServer, err := rtsp.NewServer(cfg.RTSPAddress(), creds, sps, pps)
	if err != nil {
		log.WithError(err).Fatal("failed to create RTSP server")
	}
	if cfg.TLSEnabled {
		rtspLn, err := net.Listen("tcp", cfg.RTSPAddress())
		if err != nil {
			log.WithError(err).Fatal("failed to listen on RTSP address")
		}
		rtspServer.SetListener(netutil.NewTransparentTLSListener(rtspLn, tlsCfg))
	}
	if err := rtspServer.Start(); err != nil {
		log.WithError(err).Fatal("failed to start RTSP server")
	}
	defer rtspServer.Close()
	log.WithField("addr", cfg.RTSPAddress()).Info("RTSP server listening")

	rtpEncoder, err := rtspServer.Format.CreateEncoder()
	if err != nil {
		log.WithError(err).Fatal("failed to create RTP encoder")
	}

	auHub := media.NewAUHub(sps, pps)
	rtspServer.SetSubscriber(func(bufSize int) media.Subscription {
		return auHub.Subscribe(bufSize)
	}, rtpEncoder)

	// On-demand pipeline: spins up encoder + render loop on first subscriber,
	// tears down on last unsubscribe.
	_ = media.NewPipeline(activeRenderer, ptzState, auHub, cfg.Width, cfg.Height, cfg.FPS, cfg.Bitrate)

	onvifServer := onvif.NewServer(cfg, creds, ptzState, eventsService, hostIP)
	webServer := web.NewServer(ptzState, creds, auHub, cfg.Width, cfg.Height)

	mux := http.NewServeMux()
	onvifServer.RegisterRoutes(mux)
	webServer.RegisterRoutes(mux)

	netutil.ServeHTTP(netutil.HTTPServeOptions{
		Handler:    mux,
		WebAddr:    cfg.WebAddress(),
		TLSAddr:    cfg.TLSAddress(),
		TLSConfig:  tlsCfg,
		TLSEnabled: cfg.TLSEnabled,
		TLSPort:    cfg.TLSPort,
	})

	discovery := onvif.NewDiscoveryServer(
		fmt.Sprintf("%s://%s:%d/onvif/device_service", httpScheme, hostIP, cfg.WebPort),
	)
	if err := discovery.Start(); err != nil {
		log.WithError(err).Warn("WS-Discovery failed (non-fatal)")
	} else {
		defer discovery.Stop()
	}

	log.Info("mock PTZ camera ready")
	log.Infof("RTSP:  %s://%s:%d/stream", rtspScheme, hostIP, cfg.RTSPPort)
	log.Infof("ONVIF: %s://%s:%d/onvif/device_service", httpScheme, hostIP, cfg.WebPort)
	if cfg.TLSEnabled && cfg.TLSPort != 0 {
		log.Infof("Web UI: http://%s:%d/  https://%s:%d/", hostIP, cfg.WebPort, hostIP, cfg.TLSPort)
	} else {
		log.Infof("Web UI: %s://%s:%d/", httpScheme, hostIP, cfg.WebPort)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Info("shutting down...")
}
