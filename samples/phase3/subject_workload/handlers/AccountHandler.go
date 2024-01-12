package handlers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"

	// dasvid lib
	"github.com/hpe-usp-spire/signed-assertions/phase3/subject_workload/local"
	"github.com/hpe-usp-spire/signed-assertions/phase3/subject_workload/models"
	dasvid "github.com/hpe-usp-spire/signed-assertions/poclib/svid"

	// LSVID pkg
	lsvid "github.com/hpe-usp-spire/signed-assertions/lsvid"
)

func AccountHandler(w http.ResponseWriter, r *http.Request) {

	defer timeTrack(time.Now(), "Account Handler")

	log.Print("Contacting Assertingwl to retrieve assertion... ")

	receivedAssertion := getdasvid(os.Getenv("oauthtoken"))
	err := json.Unmarshal([]byte(receivedAssertion), &temp)
	if err != nil {
		log.Fatalf("error:", err)
	}
	log.Print("Received Assertion: ", receivedAssertion)
	if (*temp.OauthSigValidation == false) || (*temp.OauthExpValidation == false) {

		returnmsg := "Oauth token validation error"

		Data = models.PocData{
			AppURI:          os.Getenv("HOSTIP"),
			Profile:         getProfileData(r),
			IsAuthenticated: isAuthenticated(r),
			Returnmsg:       returnmsg,
		}

		log.Printf(returnmsg)
		local.Tpl.ExecuteTemplate(w, "home.gohtml", Data)

	} else {

		os.Setenv("DASVIDToken", temp.DASVIDToken)
		os.Setenv("IDArtifacts", temp.IDArtifacts)

		Data = models.PocData{
			AppURI:          os.Getenv("HOSTIP"),
			Profile:         getProfileData(r),
			IsAuthenticated: isAuthenticated(r),
			DASVIDToken:     temp.DASVIDToken,
			//	DASVIDClaims:    dasvidclaims,
			HaveDASVID:    haveDASVID(),
			SigValidation: fmt.Sprintf("%v", temp.OauthSigValidation),
			ExpValidation: fmt.Sprintf("%v", temp.OauthExpValidation),
		}

		local.Tpl.ExecuteTemplate(w, "account.gohtml", Data)
	}
}

func getdasvid(oauthtoken string) string {

	defer timeTrack(time.Now(), "Get DASVID")

	// Asserting workload will validate oauth token, so we dont need to do it here.
	// stablish mtls with asserting workload and call mint endpoint, passing oauth token
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a `workloadapi.X509Source`, it will connect to Workload API using provided socket path
	source, err := workloadapi.NewX509Source(ctx, workloadapi.WithClientOptions(workloadapi.WithAddr(os.Getenv("SOCKET_PATH"))))
	if err != nil {
		log.Fatalf("Unable to create X509Source %v", err)
	}
	defer source.Close()

	// Allowed SPIFFE ID
	serverID := spiffeid.RequireTrustDomainFromString(os.Getenv("TRUST_DOMAIN"))

	// Create a `tls.Config` to allow mTLS connections, and verify that presented certificate match allowed SPIFFE ID rule
	tlsConfig := tlsconfig.MTLSClientConfig(source, source, tlsconfig.AuthorizeMemberOf(serverID))
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	////////// EXTEND LSVID ////////////
	// Fetch subject workload data
	subjectSVID	:= dasvid.FetchX509SVID()
	subjectID := subjectSVID.ID.String()
	subjectKey := subjectSVID.PrivateKey

	// Fetch subject workload LSVID
	subjectLSVID, err := lsvid.FetchLSVID(ctx, os.Getenv("SOCKET_PATH"))
	if err != nil {
		log.Fatalf("Error fetching LSVID: %v\n", err)
	}

	// Decode subject wl LSVID
	decSubjectLsvid, err := lsvid.Decode(subjectLSVID)
	if err != nil {
		log.Fatalf("Unable to decode LSVID %v\n", err)
	}

	// Get asserting WL's SPIFFE ID
	conf := &tls.Config{
		InsecureSkipVerify: true,
	}
	conn, err := tls.Dial("tcp", os.Getenv("ASSERTINGWLIP"), conf)
	if err != nil {
		log.Println("Error in Dial", err)
		return ""
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	assertingId, err := x509svid.IDFromCert(certs[0])

	// Create payload
	extendedPayload := &lsvid.Payload{
		Ver:	1,
		Alg:	"ES256",
		Iat:	time.Now().Round(0).Unix(),
		Iss:	&lsvid.IDClaim{
			CN:	subjectID,
			ID:	decSubjectLsvid.Token,
		},
		Aud:	&lsvid.IDClaim{
			CN:	assertingId.String(), //
		},
	}

	// Extend using payload
	extendedLSVID, err := lsvid.Extend(decSubjectLsvid, extendedPayload, subjectKey)
	if err != nil {
		log.Fatal("Error extending LSVID: %v\n", err)
	} 

	log.Printf("Extended LSVID: ", fmt.Sprintf("%s",extendedLSVID))
	////////////////

	var endpoint string
	token := os.Getenv("oauthtoken")
	log.Println("OAuth Token: ", token)
	endpoint = "https://" + os.Getenv("ASSERTINGWLIP") + "/extendlsvid?AccessToken=" + token + "&LSVID=" + extendedLSVID
	log.Println(endpoint)

	r, err := client.Get(endpoint)
	if err != nil {
		log.Fatalf("Error connecting to %q: %v", os.Getenv("ASSERTINGWLIP"), err)
	}

	defer r.Body.Close()
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Fatalf("Unable to read body: %v", err)
	}
	log.Println("Asserting-wl response: ", fmt.Sprintf("%s", body))
	return fmt.Sprintf("%s", body)
}
