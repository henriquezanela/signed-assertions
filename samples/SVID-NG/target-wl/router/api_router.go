package router

import (
	"context"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/hpe-usp-spire/signed-assertions/SVID-NG/target-wl/handlers"
)

func TargetWLRouter(ctx context.Context) (*mux.Router, error) {

	s := mux.NewRouter()

	s.HandleFunc("/get_balance", handlers.GetBalanceHandler).Methods("GET")
	s.HandleFunc("/deposit", handlers.DepositHandler).Methods("GET")

	s.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	return s, nil
}
