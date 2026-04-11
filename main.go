package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	log "github.com/sirupsen/logrus"

	"github.com/ohcnetwork/mock-ptz-camera/auth"
	"github.com/ohcnetwork/mock-ptz-camera/config"
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
		hostIP = detectHostIP()
		log.WithField("ip", hostIP).Info("detected host IP")
	} else {
		log.WithField("ip", hostIP).Info("using HOST_IP override")
	}

	eventsService := onvif.NewEventsService(
		fmt.Sprintf("http://%s:%d/onvif/subscription", hostIP, cfg.WebPort),
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

	encoder, err := renderer.NewEncoder(cfg.Width, cfg.Height, cfg.FPS, cfg.Bitrate)
	if err != nil {
		log.WithError(err).Fatal("failed to start encoder")
	}

	// Send a blank frame to extract SPS/PPS from the encoder
	blankFrame := make([]byte, cfg.Width*cfg.Height*3)
	if err := encoder.WriteFrame(blankFrame); err != nil {
		log.WithError(err).Fatal("failed to write blank frame")
	}
	encoder.WaitSPSPPS()
	sps, pps := encoder.SPS(), encoder.PPS()
	log.WithFields(log.Fields{
		"sps_bytes": len(sps), "pps_bytes": len(pps),
	}).Debug("got SPS/PPS from encoder")

	// Drain stale AU from the blank frame, then stop the bootstrap encoder.
	select {
	case <-encoder.AccessUnits():
	default:
	}
	encoder.Stop()

	rtspServer, err := rtsp.NewServer(cfg.RTSPAddress(), creds, sps, pps)
	if err != nil {
		log.WithError(err).Fatal("failed to create RTSP server")
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

	auHub := web.NewAUHub(sps, pps)

	// Wire RTSP server to subscribe/unsubscribe from AUHub on session play/close.
	rtspServer.SetSubscriber(func(bufSize int) rtsp.Subscription {
		return auHub.Subscribe(bufSize)
	}, rtpEncoder)

	// Pipeline starts/stops the encoder and render loop on demand
	// when the first/last AUHub subscriber arrives/leaves.
	_ = NewPipeline(activeRenderer, ptzState, auHub, cfg.Width, cfg.Height, cfg.FPS, cfg.Bitrate)

	onvifServer := onvif.NewServer(cfg, creds, ptzState, eventsService, hostIP)
	webServer := web.NewServer(ptzState, creds, auHub, cfg.Width, cfg.Height)

	mux := http.NewServeMux()
	onvifServer.RegisterRoutes(mux)
	webServer.RegisterRoutes(mux)

	httpServer := &http.Server{
		Addr:    cfg.WebAddress(),
		Handler: mux,
	}
	go func() {
		log.WithField("addr", cfg.WebAddress()).Info("web server listening")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("web server error")
		}
	}()

	discovery := onvif.NewDiscoveryServer(
		fmt.Sprintf("http://%s:%d/onvif/device_service", hostIP, cfg.WebPort),
	)
	if err := discovery.Start(); err != nil {
		log.WithError(err).Warn("WS-Discovery failed (non-fatal)")
	} else {
		defer discovery.Stop()
	}

	log.Info("mock PTZ camera ready")
	log.Infof("RTSP: rtsp://%s:%d/stream", hostIP, cfg.RTSPPort)
	log.Infof("ONVIF: http://%s:%d/onvif/device_service", hostIP, cfg.WebPort)
	log.Infof("Web UI: http://%s:%d/", hostIP, cfg.WebPort)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Info("shutting down...")
}

func detectHostIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "127.0.0.1"
}
