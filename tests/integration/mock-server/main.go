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
	"time"

	"github.com/nebius/gosdk/proto/nebius/logging/v1/agentmanager"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
)

const metadataHeaderValue = "true"

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

func setupControlAPI(svc *mockVersionService) *http.ServeMux {
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
		log.Printf("Response set: action=%s", resp.GetAction().String())
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
		_ = json.NewEncoder(w).Encode(jsonReqs)
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
		_, _ = w.Write(data)
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

	return mux
}

func requireMetadataHeader(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Metadata") != metadataHeaderValue {
		http.Error(w, "missing Metadata header", http.StatusForbidden)
		return false
	}
	return true
}

func setupIMDSMock() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/instance-data", func(w http.ResponseWriter, r *http.Request) {
		if !requireMetadataHeader(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": "test-instance-id", "parent_id": "test-parent-id"}`))
	})

	mux.HandleFunc("/v1/instance-data/o11y/updater-endpoint-override", func(w http.ResponseWriter, r *http.Request) {
		if !requireMetadataHeader(w, r) {
			return
		}
		_, _ = w.Write([]byte("mock-server:50051"))
	})

	mux.HandleFunc("/v1/iam/tsa/token/access_token", func(w http.ResponseWriter, r *http.Request) {
		if !requireMetadataHeader(w, r) {
			return
		}
		_, _ = w.Write([]byte("test-tsa-token"))
	})

	mux.HandleFunc("/v1/iam/tsa/token/expires_at", func(w http.ResponseWriter, r *http.Request) {
		if !requireMetadataHeader(w, r) {
			return
		}
		expiresAt := time.Now().Add(12 * time.Hour).UTC().Format(time.RFC3339Nano)
		_, _ = w.Write([]byte(expiresAt))
	})

	return mux
}

func main() {
	svc := newMockVersionService()

	// Start gRPC server
	var lc net.ListenConfig
	grpcLis, err := lc.Listen(context.Background(), "tcp", ":50051")
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

	// Start IMDS mock server on port 80
	go func() {
		log.Println("IMDS mock listening on :80")
		if err := http.ListenAndServe(":80", setupIMDSMock()); err != nil {
			log.Fatalf("IMDS mock serve error: %v", err)
		}
	}()

	// Start HTTP control API
	log.Println("HTTP control API listening on :8080")
	if err := http.ListenAndServe(":8080", setupControlAPI(svc)); err != nil {
		log.Fatalf("HTTP serve error: %v", err)
	}
}
