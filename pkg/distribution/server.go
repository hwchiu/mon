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

// Server implements the Distribution service for pushing policy updates to agents.
type Server struct {
	pb.UnimplementedDistributionServer

	mu     sync.RWMutex
	agents map[string]*AgentStatus // key: agentID
	// TODO: in real, hold reference to the policy engine or store to get latest PolicyVersion per zone.
}

func NewServer() *Server {
	return &Server{
		agents: make(map[string]*AgentStatus),
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

func (s *Server) StreamUpdates(req *pb.RegisterRequest, stream pb.Distribution_StreamUpdatesServer) error {
	log.Printf("Starting policy stream for agent %s in zone %s", req.AgentId, req.ZoneId)
	// TODO: in real, watch for new PolicyVersion for the zone, serialize map data, sign, send.
	// For skeleton, just send a hello once.
	update := &pb.PolicyUpdate{
		Version: "v-skeleton-1",
		ZoneId:  req.ZoneId,
		MapData: []byte("stub-map-data-here"),
		// Signature would be added with real key.
	}
	if err := stream.Send(update); err != nil {
		return err
	}
	<-stream.Context().Done()
	return nil
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
