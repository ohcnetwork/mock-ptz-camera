package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	log "github.com/sirupsen/logrus"

	"github.com/ohcnetwork/mock-ptz-camera/auth"
	"github.com/ohcnetwork/mock-ptz-camera/config"
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

	// ---- TLS setup ----
	var tlsCfg *tls.Config
	if cfg.TLSEnabled {
		certFile := cfg.TLSCertFile
		keyFile := cfg.TLSKeyFile
		if certFile == "" || keyFile == "" {
			certFile = filepath.Join(cfg.TLSCertDir, "server.crt")
			keyFile = filepath.Join(cfg.TLSCertDir, "server.key")
		}
		cert, err := netutil.LoadOrGenerateCert(certFile, keyFile, []string{hostIP})
		if err != nil {
			log.WithError(err).Fatal("failed to load/generate TLS certificate")
		}
		tlsCfg = netutil.NewTLSConfig(cert)
		log.WithFields(log.Fields{
			"cert": certFile, "key": keyFile,
		}).Info("TLS enabled")
	}

	// ---- RTSP server ----
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
		Handler: mux,
	}

	// ---- Web server with optional TLS ----
	switch {
	case !cfg.TLSEnabled:
		// Plain HTTP only.
		httpServer.Addr = cfg.WebAddress()
		netutil.ServeAsync("HTTP", cfg.WebAddress(), httpServer.ListenAndServe)

	case cfg.TLSPort == 0:
		// TLS mux: serve both HTTP and HTTPS on the same port.
		webLn, err := net.Listen("tcp", cfg.WebAddress())
		if err != nil {
			log.WithError(err).Fatal("failed to listen on web address")
		}
		split := netutil.NewSplitListener(webLn)
		go split.Serve()

		httpsServer := &http.Server{Handler: mux, TLSConfig: tlsCfg}
		netutil.ServeAsync("HTTP+HTTPS mux", cfg.WebAddress(), func() error {
			return httpsServer.ServeTLS(split.TLS(), "", "")
		})
		netutil.ServeAsync("", "", func() error {
			return httpServer.Serve(split.Plain())
		})

	default:
		// Separate ports: HTTP on WebPort, HTTPS on TLSPort.
		httpServer.Addr = cfg.WebAddress()
		netutil.ServeAsync("HTTP", cfg.WebAddress(), httpServer.ListenAndServe)

		httpsServer := &http.Server{Addr: cfg.TLSAddress(), Handler: mux, TLSConfig: tlsCfg}
		netutil.ServeAsync("HTTPS", cfg.TLSAddress(), func() error {
			return httpsServer.ListenAndServeTLS("", "")
		})
	}

	// ---- ONVIF discovery ----
	discovery := onvif.NewDiscoveryServer(
		fmt.Sprintf("%s://%s:%d/onvif/device_service", httpScheme, hostIP, cfg.WebPort),
	)
	if err := discovery.Start(); err != nil {
		log.WithError(err).Warn("WS-Discovery failed (non-fatal)")
	} else {
		defer discovery.Stop()
	}

	// ---- Ready ----
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
