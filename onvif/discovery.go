package onvif

import (
	"fmt"
	"net"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const wsDiscoveryAddr = "239.255.255.250:3702"

type DiscoveryServer struct {
	deviceURL string
	stopCh    chan struct{}
}

func NewDiscoveryServer(deviceURL string) *DiscoveryServer {
	return &DiscoveryServer{
		deviceURL: deviceURL,
		stopCh:    make(chan struct{}),
	}
}

func (ds *DiscoveryServer) Start() error {
	addr, err := net.ResolveUDPAddr("udp4", wsDiscoveryAddr)
	if err != nil {
		return fmt.Errorf("resolve multicast addr: %w", err)
	}

	conn, err := net.ListenMulticastUDP("udp4", nil, addr)
	if err != nil {
		return fmt.Errorf("listen multicast: %w", err)
	}
	conn.SetReadBuffer(8192)

	log.WithField("addr", wsDiscoveryAddr).Info("WS-Discovery listening")
	go ds.readLoop(conn)
	return nil
}

func (ds *DiscoveryServer) Stop() {
	close(ds.stopCh)
}

func (ds *DiscoveryServer) readLoop(conn *net.UDPConn) {
	defer conn.Close()
	buf := make([]byte, 8192)
	for {
		select {
		case <-ds.stopCh:
			return
		default:
		}
		conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
				continue
			}
			log.WithError(err).Error("WS-Discovery read error")
			continue
		}
		msg := string(buf[:n])
		if isProbeMessage(msg) {
			msgID := extractMessageID(msg)
			log.WithFields(log.Fields{"from": remoteAddr, "messageID": msgID}).Debug("WS-Discovery probe received")
			resp := ds.buildProbeMatch(msgID)
			conn.WriteToUDP([]byte(resp), remoteAddr)
		}
	}
}

func isProbeMessage(msg string) bool {
	return strings.Contains(msg, "Probe") && strings.Contains(msg, "schemas.xmlsoap.org/ws/2005/04/discovery")
}

func extractMessageID(msg string) string {
	start := strings.Index(msg, "<a:MessageID>")
	if start < 0 {
		start = strings.Index(msg, "<wsa:MessageID>")
		if start < 0 {
			return ""
		}
		start += len("<wsa:MessageID>")
	} else {
		start += len("<a:MessageID>")
	}
	end := strings.IndexAny(msg[start:], "<")
	if end < 0 {
		return ""
	}
	return msg[start : start+end]
}

func (ds *DiscoveryServer) buildProbeMatch(relatesTo string) string {
	msgID := fmt.Sprintf("urn:uuid:mock-ptz-%d", time.Now().UnixNano())
	return renderTemplate("probeMatch", probeMatchData{
		MessageID: msgID,
		RelatesTo: relatesTo,
		DeviceURL: ds.deviceURL,
	})
}
