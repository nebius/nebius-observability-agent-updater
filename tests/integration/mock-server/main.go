package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"

	"github.com/nebius/gosdk/proto/nebius/logging/v1/agentmanager"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
)

type mockVersionService struct {
	agentmanager.UnimplementedVersionServiceServer
	mu       sync.Mutex
	requests []*agentmanager.GetVersionRequest
	response *agentmanager.GetVersionResponse
}

func newMockVersionService() *mockVersionService {
	return &mockVersionService{
		response: &agentmanager.GetVersionResponse{
			Action: agentmanager.Action_NOP,
		},
	}
}

func (s *mockVersionService) GetVersion(_ context.Context, req *agentmanager.GetVersionRequest) (*agentmanager.GetVersionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	log.Printf("GetVersion called: parent_id=%s instance_id=%s agent_type=%s", req.GetParentId(), req.GetInstanceId(), req.GetType().String())
	s.requests = append(s.requests, req)
	return s.response, nil
}

func (s *mockVersionService) setResponse(resp *agentmanager.GetVersionResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.response = resp
}

func (s *mockVersionService) getRequests() []*agentmanager.GetVersionRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make([]*agentmanager.GetVersionRequest, len(s.requests))
	copy(copied, s.requests)
	return copied
}

func (s *mockVersionService) getLatestRequest() *agentmanager.GetVersionRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return nil
	}
	return s.requests[len(s.requests)-1]
}

func (s *mockVersionService) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = nil
	s.response = &agentmanager.GetVersionResponse{
		Action: agentmanager.Action_NOP,
	}
}

func main() {
	svc := newMockVersionService()

	// Start gRPC server
	grpcLis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("failed to listen on :50051: %v", err)
	}
	grpcServer := grpc.NewServer()
	agentmanager.RegisterVersionServiceServer(grpcServer, svc)
	go func() {
		log.Println("gRPC server listening on :50051")
		if err := grpcServer.Serve(grpcLis); err != nil {
			log.Fatalf("gRPC serve error: %v", err)
		}
	}()

	// Start HTTP control API
	mux := http.NewServeMux()

	mux.HandleFunc("/api/response", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("read body: %v", err), http.StatusBadRequest)
			return
		}
		resp := &agentmanager.GetVersionResponse{}
		if err := protojson.Unmarshal(body, resp); err != nil {
			http.Error(w, fmt.Sprintf("unmarshal: %v", err), http.StatusBadRequest)
			return
		}
		svc.setResponse(resp)
		log.Printf("Response set: action=%s feature_flags=%v", resp.GetAction().String(), resp.GetFeatureFlags())
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	mux.HandleFunc("/api/requests", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		reqs := svc.getRequests()
		var jsonReqs []json.RawMessage
		for _, req := range reqs {
			data, err := protojson.Marshal(req)
			if err != nil {
				http.Error(w, fmt.Sprintf("marshal: %v", err), http.StatusInternalServerError)
				return
			}
			jsonReqs = append(jsonReqs, data)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jsonReqs)
	})

	mux.HandleFunc("/api/request/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		req := svc.getLatestRequest()
		if req == nil {
			http.Error(w, "no requests received", http.StatusNotFound)
			return
		}
		data, err := protojson.Marshal(req)
		if err != nil {
			http.Error(w, fmt.Sprintf("marshal: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})

	mux.HandleFunc("/api/clear", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		svc.clear()
		log.Println("State cleared")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	log.Println("HTTP control API listening on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatalf("HTTP serve error: %v", err)
	}
}
