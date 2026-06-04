package distribution

import (
	"context"
	"log"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/hwchiu/mon/pkg/proto/dfw/v1"
)

// AgentStatus tracks connected agents for the frontend/status.
type AgentStatus struct {
	AgentID   string
	ZoneID    string
	Version   string
	Healthy   bool
	LastSeen  time.Time
}

// connectedAgent holds live stream for push.
type connectedAgent struct {
	agentID string
	zoneID  string
	stream  pb.Distribution_StreamUpdatesServer
	sendCh  chan *pb.PolicyUpdate // for non-blocking push from controller
}

// Server implements the Distribution service for pushing policy updates to agents.
type Server struct {
	pb.UnimplementedDistributionServer

	mu       sync.RWMutex
	agents   map[string]*AgentStatus // key: agentID (for status/frontend)
	conns    map[string]*connectedAgent // live streams by agentID
	latest   map[string]*pb.PolicyUpdate // latest per zone (for new connects + recovery)
	// TODO: signer for real sigs.
}

func NewServer() *Server {
	return &Server{
		agents: make(map[string]*AgentStatus),
		conns:  make(map[string]*connectedAgent),
		latest: make(map[string]*pb.PolicyUpdate),
	}
}

func (s *Server) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	log.Printf("Agent registered: %s in zone %s, current version %s", req.AgentId, req.ZoneId, req.CurrentVersion)
	s.mu.Lock()
	s.agents[req.AgentId] = &AgentStatus{
		AgentID:  req.AgentId,
		ZoneID:   req.ZoneId,
		Version:  req.CurrentVersion,
		Healthy:  true,
		LastSeen: time.Now(),
	}
	s.mu.Unlock()
	return &pb.RegisterResponse{AssignedZone: req.ZoneId}, nil
}

// PushUpdate is called by the controller (after new PolicyVersion from engine) to push to agents in a zone.
// It sends to all currently connected agents for that zone (fan-out). Non-blocking per agent.
func (s *Server) PushUpdate(zoneID, version string, mapData, signature []byte) {
	upd := &pb.PolicyUpdate{
		Version:   version,
		ZoneId:    zoneID,
		MapData:   mapData,
		Signature: signature,
	}

	s.mu.Lock()
	s.latest[zoneID] = upd
	// fanout
	for _, ca := range s.conns {
		if ca.zoneID == zoneID {
			select {
			case ca.sendCh <- upd:
			default:
				log.Printf("push channel full for %s, dropping update %s", ca.agentID, version)
			}
		}
	}
	s.mu.Unlock()
	log.Printf("Pushed policy %s to zone %s (map len=%d)", version, zoneID, len(mapData))
}

func (s *Server) StreamUpdates(req *pb.RegisterRequest, stream pb.Distribution_StreamUpdatesServer) error {
	log.Printf("Starting policy stream for agent %s in zone %s", req.AgentId, req.ZoneId)

	ca := &connectedAgent{
		agentID: req.AgentId,
		zoneID:  req.ZoneId,
		stream:  stream,
		sendCh:  make(chan *pb.PolicyUpdate, 8),
	}

	s.mu.Lock()
	s.conns[req.AgentId] = ca
	// send latest for the zone immediately if we have one (helps recovery / late join)
	if latest, ok := s.latest[req.ZoneId]; ok {
		_ = stream.Send(latest) // best effort
	}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.conns, req.AgentId)
		s.mu.Unlock()
	}()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case upd, ok := <-ca.sendCh:
			if !ok {
				return nil
			}
			if err := stream.Send(upd); err != nil {
				return err
			}
			// also update our status view
			s.mu.Lock()
			if st, ok := s.agents[req.AgentId]; ok {
				st.Version = upd.Version
				st.LastSeen = time.Now()
			}
			s.mu.Unlock()
		}
	}
}

func (s *Server) ReportStatus(ctx context.Context, report *pb.StatusReport) (*emptypb.Empty, error) {
	log.Printf("Status from %s: version=%s healthy=%v", report.AgentId, report.Version, report.Healthy)
	s.mu.Lock()
	if st, ok := s.agents[report.AgentId]; ok {
		st.Version = report.Version
		st.Healthy = report.Healthy
		st.LastSeen = time.Now()
	}
	s.mu.Unlock()
	return &emptypb.Empty{}, nil
}

// GetAgents returns a snapshot of connected agents (for frontend/status).
func (s *Server) GetAgents() []AgentStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]AgentStatus, 0, len(s.agents))
	for _, a := range s.agents {
		res = append(res, *a)
	}
	return res
}

// StartGRPCServer starts the gRPC server on the given address (e.g. ":9443").
func StartGRPCServer(addr string) *Server {
	srv := NewServer()
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterDistributionServer(grpcServer, srv)
	log.Printf("DFW Distribution gRPC server listening on %s", addr)
	go grpcServer.Serve(lis)
	return srv
}
