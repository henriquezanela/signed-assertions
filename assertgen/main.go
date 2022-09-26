package main

import (
	"context"
	"io/ioutil"
	"log"
	"net/http"
	"net"
	"fmt"
	"os"
	"crypto"
	"encoding/base64"
	"encoding/json"
	"crypto/sha256"
	"crypto/rand"
	"time"
	"crypto/rsa"
	"crypto/ecdsa"
	"math/big"
	"strings"
	"strconv"
	"bytes"	
	"crypto/x509"
	"encoding/pem"
	"bufio"
	
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/spiffe/go-spiffe/v2/workloadapi"

	// dasvid lib
	dasvid "github.com/marco-developer/dasvid/poclib"

	// Schnorr support
	"go.dedis.ch/kyber/v3"
	"go.dedis.ch/kyber/v3/group/edwards25519"
)

const (
	// Workload API socket path
	socketPath	= "unix:///tmp/spire-agent/public/api.sock"
	
)

var curve = edwards25519.NewBlakeSHA256Ed25519()

type keydata struct {
	Kid			string `json:kid",omitempty"`
	Alg			string `json:alg",omitempty"`
	Pkey		[]byte `json:pkey",omitempty"`
	Exp			int64  `json:exp",omitempty"`
}

func GetOutboundIP() net.IP {
    conn, err := net.Dial("udp", "8.8.8.8:80")
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()

    localAddr := conn.LocalAddr().(*net.UDPAddr)

    return localAddr.IP
}

func main() {
	ParseEnvironment()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var endpoint string
	
	// Retrieve local IP
	// In this PoC example, client and server are running in the same host, so serverIP = clientIP 
	StrIPlocal := fmt.Sprintf("%v", GetOutboundIP())
	serverURL := StrIPlocal + ":8443"

	operation := os.Args[1]

	// Create a `workloadapi.X509Source`, it will connect to Workload API using provided socket path
	source, err := workloadapi.NewX509Source(ctx, workloadapi.WithClientOptions(workloadapi.WithAddr(socketPath)))
	if err != nil {
		log.Fatalf("Unable to create X509Source %v", err)
	}
	defer source.Close()

	// Allowed SPIFFE ID
	serverID := spiffeid.RequireTrustDomainFromString("example.org")

	// Create a `tls.Config` to allow mTLS connections, and verify that presented certificate match allowed SPIFFE ID rule
	tlsConfig := tlsconfig.MTLSClientConfig(source, source, tlsconfig.AuthorizeMemberOf(serverID))
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	switch operation {

	case "help":
		fmt.Printf(`
		
Description:
 Client to interact with SPIRE and Asserting WL, also useful to mint assertions. 
 Developed to assertions and tokens demo.

Main functions:

  - print
	Print informed nest token
	usage: ./assertgen print token
  - mint
  	Ask running asserting-wl for a new DASVID given Oauthtoken
	usage: ./assertgen mint OAuthtoken
  - keys
	Ask asserting-wl Public Key
	usage: ./assertgen keys
  - validate
  	Ask asserting-wl for DASVID validation (signature/expiration)
    usage: ./assertgen validate DASVID
  - zkp
  	Ask for ZKP given DASVID
    usage: ./assertgen zkp DASVID
  - traceadd
      Add next hop assertion to existing token 
	  usage: ./assertgen traceadd <originaltoken> secretkey nextsecretkey
  - ecdsagen
      Generate a new ECDSA assertion
	  usage: ./assertgen ecdsagen <assertionkey> <assertion_value> <spiffeid/svid>
  - ecdsaver
  	  Verify assertion signature
      usage: ./assertgen verify direction assertion
      extract the keyid from token and use it to retrieve public key from IdP
  - append
	  Append assertion to an existing token
	  usage: ./assertgen append originaltoken assertionKey assertionValue spiffeid/svid
	  Changed "alg" to "kid", that is used to retrieve correct key informations from IdP 
	  kid = public key hash
  - multiappend
	  Append assertion to an existing token
	  usage: ./assertgen append originaltoken assertionKey assertionValue spiffeid/svid
	  Changed "alg" to "kid", that is used to retrieve correct key informations from IdP 
	  kid = public key hash
  - schgen
	  Generate a new schnorr signed assertion
	  usage: ./main schgen assertionKey assertionValue
  - schver
	  Verify assertion schnorr signature
	  usage: ./assertgen schver assertion 
  - appsch
  	  Appent an assertion with schnorr signature
  	  usage: ./main appsch originaltoken assertionKey assertionValue
`)
	os.Exit(1)

	case "print":
		// 	Print given token
		//  usage: ./main print token
		token := os.Args[2]
		printtoken(token)
		os.Exit(1)
	
    case "mint":
		// 	Ask asserting-wl for a new minted DASVID
		//  usage: ./assertgen mint OAuthtoken
		token := os.Args[2]
		endpoint = "https://"+serverURL+"/mint?AccessToken="+token

    case "keys":
		// 	Ask asserting-wl Public Key
		//  usage: ./assertgen keys
		endpoint = "https://"+serverURL+"/keys"

    case "validate":
		// 	Ask asserting-wl for DASVID validation (signature/expiration)
		//  usage: ./assertgen validate DASVID
		dasvid := os.Args[2]
		endpoint = "https://"+serverURL+"/validate?DASVID="+dasvid

	case "zkp":
		// 	Ask for ZKP given DASVID
		//  usage: ./assertgen zkp DASVID
		dasvid := os.Args[2]
		endpoint = "https://"+serverURL+"/introspect?DASVID="+dasvid
	
	case "ecdsagen":
		// Generate a new assertion
		// usage: ./main generic assertionKey assertionValue spiffeid/svid

		// Fetch claims data
		clientSVID 		:= dasvid.FetchX509SVID()
		clientID 		:= clientSVID.ID.String()
		clientkey 		:= clientSVID.PrivateKey

		// timestamp
		issue_time 		:= time.Now().Round(0).Unix()

		// assertion key:value
		assertionkey 	:= os.Args[2]
		assertionvalue 	:= os.Args[3]

		// uses spiffeid or svid as issuer
		svidAsIssuer 	:= os.Args[4]

		// generate encoded key
		pubkey := clientkey.Public().(*ecdsa.PublicKey)
		encKey, _ := EncodeECDSAPublicKey(pubkey)

		//  Define issuer type:
		var issuer string
		switch svidAsIssuer {
		case "spiffeid":
			// Uses SPIFFE-ID as ISSUER
			issuer = clientID
		case "svid":
			// Uses SVID cert bundle as ISSUER
			tmp, _, err := clientSVID.Marshal()
			if err != nil {
				fmt.Println("Error retrieving SVID: ", err)
				os.Exit(1)
			}
			issuer = fmt.Sprintf("%s", tmp)
		case "anonymous":
			// Uses public key as ISSUER
			issuer = fmt.Sprintf("%s", encKey)

		default:
			fmt.Println("Error defining issuer! Select spiffeid or svid.")
			os.Exit(1)
		}
		
		// Define assertion claims
		kid 			:= base64.RawURLEncoding.EncodeToString([]byte(clientID))
		assertionclaims := map[string]interface{}{
			"iss"		:		issuer,
			"iat"		:	 	issue_time,
			"kid"		:		kid,
			assertionkey:		assertionvalue,
		}
		assertion, err := newencode(assertionclaims, "", clientkey)
		if err != nil {
			fmt.Println("Error generating signed assertion!")
			os.Exit(1)
		} 

		fmt.Println("Generated assertion: ", fmt.Sprintf("%s",assertion))

		//  save public key in IdP
		key := &keydata{
			Kid		:	kid,
			Alg		:	"EC256",
			Pkey	:	encKey,
			Exp		:	time.Now().Add(time.Hour * 1).Round(0).Unix(),
		}
		mkey, _ := json.Marshal(key)
		savekey, err := addkey(fmt.Sprintf("%s",mkey))
		if err != nil {
			fmt.Errorf("error: %s", err)
			os.Exit(1)
		}
		fmt.Println("Key successfully stored: ", savekey)


		os.Exit(1)

	case "ecdsaver":
		// 	Verify assertion signature
		//  usage: ./assertgen verify direction assertion
		//  extract the keyid from token and use it to retrieve public key from IdP

		// direction := os.Args[2]
		assertion := os.Args[2]

		// if (direction=="reverse") {
		// 	validatreverse(assertion, pubkey.(*ecdsa.PublicKey))
		// }
		// if (direction=="direct") {
			validateassertion(assertion)
		// }
		os.Exit(1)

	case "append":
		// Append assertion to an existing token
		//  usage: ./assertgen append originaltoken assertionKey assertionValue spiffeid/svid
		// Changed "alg" to "kid", that is used to retrieve correct key informations from IdP 
		// kid = public key hash

		// Fetch claims data
		clientSVID 		:= dasvid.FetchX509SVID()
		clientID 		:= clientSVID.ID.String()
		clientkey 		:= clientSVID.PrivateKey

		// timestamp
		issue_time 		:= time.Now().Round(0).Unix()

		// main token and assertion values
		mainvalue	 	:= os.Args[2]
		assertionkey 	:= os.Args[3]
		assertionvalue 	:= os.Args[4]

		// uses spiffeid or svid as token/assertion issuer
		svidAsIssuer 	:= os.Args[5]

		// validate main token before appending
		pubkey 			:= clientkey.Public()
		encKey, _ 		:= EncodeECDSAPublicKey(pubkey.(*ecdsa.PublicKey))
		valid 			:= validateassertion(mainvalue)
		if valid != true{
			fmt.Println("Cannot append: Invalid assertion!")
			os.Exit(1)
		}

		//  Define issuer type:
		var issuer string
		switch svidAsIssuer {
			case "spiffeid":
				// Uses SPIFFE-ID as ISSUER
				issuer = clientID
			case "svid":
				// Uses SVID cert bundle as ISSUER
				tmp, _, err := clientSVID.Marshal()
				if err != nil {
					fmt.Println("Error retrieving SVID: ", err)
					os.Exit(1)
				}
				issuer = fmt.Sprintf("%s", tmp)
			case "anonymous":
				//Uses public key as ISSUER
				issuer = fmt.Sprintf("%s", encKey)

			default:
				fmt.Println("Error defining issuer! Select spiffeid or svid.")
				os.Exit(1)
		}

			// Define token claims
			fmt.Println("**mainvalue size: ", len(mainvalue))
			fmt.Println("Other claims size: ", len(issuer)+len(assertionvalue)+len(string(issue_time)))

			kid 			:= base64.RawURLEncoding.EncodeToString([]byte(clientID))
			tokenclaims 	:= map[string]interface{}{
				"iss"		:				issuer,
				"iat"		:	 			issue_time,
				"kid"		:				kid[:],
				assertionkey:		assertionvalue,
			}
			assertion, err := newencode(tokenclaims, mainvalue, clientkey)
			if err != nil {
				fmt.Println("Error generating signed assertion!")
				os.Exit(1)
			} 

		fmt.Println("Generated assertion: ", fmt.Sprintf("%s",assertion))
		fmt.Println("Assertion size", len(assertion))

		//  save public key in IdP
		key := &keydata{
			Kid		:	kid[:],
			Alg		:	"EC256",
			Pkey	:	encKey,
			Exp		:	time.Now().Add(time.Hour * 1).Round(0).Unix(),
		}
		mkey, _ := json.Marshal(key)
		savekey, err := addkey(fmt.Sprintf("%s",mkey))
		if err != nil {
			fmt.Errorf("error: %s", err)
			os.Exit(1)
		}
		fmt.Println("Key successfully stored: ", savekey)

		os.Exit(1)

	case "multiappend":
		defer timeTrack(time.Now(), "multiappend ")
		// Append a specific number of ECDSA assertions to an existing token (for test purposes in some scenarios)
		//  usage: ./main multiappend originaltoken assertionKey assertionValue howmany spiffeid/svid

		// main token and assertion values
		mainvalue	 		:= os.Args[2]
		assertionkey 		:= os.Args[3]
		assertionvalue 		:= os.Args[4]
		manytimes, _	 	:= strconv.Atoi(os.Args[5])

		// uses spiffeid or svid as token/assertion issuer
		svidAsIssuer 	:= os.Args[6]

		i := 0 
		for i <  manytimes {

			// timestamp
			issue_time 		:= time.Now().Round(0).Unix()

			// generate encoded public key
			clientSVID 		:= dasvid.FetchX509SVID()
			clientID 		:= clientSVID.ID.String()
			clientkey 		:= clientSVID.PrivateKey
			pubkey			:= clientkey.Public().(*ecdsa.PublicKey)
			encKey, _ 		:= EncodeECDSAPublicKey(pubkey)

			//  Define issuer type:
			var issuer string
			switch svidAsIssuer {
				case "spiffeid":
					// Uses SPIFFE-ID as ISSUER
					issuer = clientID
					// fmt.Println("issuer: ", issuer)
				case "svid":
					// Uses SVID cert bundle as ISSUER
					tmp, _, err := clientSVID.Marshal()
					if err != nil {
						fmt.Println("Error retrieving SVID: ", err)
						os.Exit(1)
					}
					issuer = fmt.Sprintf("%s", tmp)
				case "anonymous":
					// Uses public key as ISSUER
					issuer = fmt.Sprintf("%s", encKey)
					
				default:
					fmt.Println("Error defining issuer! Select spiffeid or svid.")
					os.Exit(1)
			}
			
			// Define token claims
			kid 			:= base64.RawURLEncoding.EncodeToString([]byte(clientID))
			tokenclaims 	:= 	map[string]interface{}{
				"iss"		:	issuer,
				"iat"		:	issue_time,
				"kid"		:	kid[:],
				assertionkey+fmt.Sprintf("%v", i):	assertionvalue+fmt.Sprintf("%v", i),
			}
			assertion, err := newencode(tokenclaims, mainvalue, clientkey)
			if err != nil {
				fmt.Println("Error generating signed assertion!")
				os.Exit(1)
			} 

			mainvalue = fmt.Sprintf("%s", assertion)
			fmt.Printf("Resulting assertion: %s\n", mainvalue)

			//  save public key in IdP
			key := &keydata{
				Kid		:	kid[:],
				Alg		:	"EC256",
				Pkey	:	encKey,
				Exp		:	time.Now().Add(time.Hour * 1).Round(0).Unix(),
			}
			mkey, _ := json.Marshal(key)
			savekey, _ := addkey(fmt.Sprintf("%s",mkey))
			if err != nil {
				fmt.Errorf("error: %s", err)
				os.Exit(1)
			}
			fmt.Println("Key successfully stored: ", savekey)
			i++
		}

		os.Exit(1)

//  ____________ NEW _____________ //
	case "schkeypair":

		// given id
		privateKey, publicKey := dasvid.IDKeyPair(os.Args[1])
		
		// random
		// privateKey, publicKey := dasvid.RandomKeyPair()

		fmt.Println("Generated private key  : ", privateKey.String())
		fmt.Println("Generated publicKey key: ", pubkey2string(publicKey))
		os.Exit(1)	
		
	case "schgen":
		// Generate a new schnorr signed assertion containing key:value with no audience

		// usage: ./assertgen schgen assertionKey assertionValue

		// Generate Keypair
		privateKey, publicKey := dasvid.RandomKeyPair()

		// timestamp
		issue_time 		:= time.Now().Round(0).Unix()

		// assertion key:value
		assertionkey 	:= os.Args[2]
		assertionvalue 	:= os.Args[3]
		assertionclaims := map[string]interface{}{
			"iss"		:		pubkey2string(publicKey),
			"iat"		:	 	issue_time,
			assertionkey:		assertionvalue,
		}
		assertion, err := newschnorrencode(assertionclaims, "", privateKey)
		if err != nil {
			fmt.Println("Error generating signed schnorr assertion!")
			os.Exit(1)
		} 

		fmt.Println("Generated assertion: ", fmt.Sprintf("%s",assertion))
		os.Exit(1)

	case "tracenew":
		// Generate a new schnorr signed assertion containing key:value and audience
		// issuer: public key from secretkey
		// audience: public key from nextsecretkey

		// usage: ./assertgen tracenew assertionKey assertionValue secretkey nextsecretkey
		// secretkey     : KeyID used to sign assertion. 
		// nextsecretkey : next hop private KeyID.

		// Generate Keypair given secretkey
		privateKey, publicKey := dasvid.IDKeyPair(os.Args[4])

		// Generate nextpublicKey given nextsecretkey
		_, nextpublicKey := dasvid.IDKeyPair(os.Args[5])

		// timestamp
		issue_time 		:= time.Now().Round(0).Unix()

		// assertion claims
		assertionclaims := map[string]interface{}{
			"iss"		:		pubkey2string(publicKey),
			"aud"		:	 	pubkey2string(nextpublicKey),
			"iat"		:	 	issue_time,
			os.Args[2]  :		os.Args[3],
		}
		// encode and sign assertion
		assertion, err := newschnorrencode(assertionclaims, "", privateKey)
		if err != nil {
			fmt.Println("Error generating signed schnorr assertion!")
			os.Exit(1)
		} 

		fmt.Println("Generated assertion: ", fmt.Sprintf("%s",assertion))
		os.Exit(1)

	case "traceadd":
		// 	Add next hop assertion to existing token
		//  usage: ./assertgen next oldmain sourceprivatekey destinyprivatekey

		// timestamp
		issue_time 		:= time.Now().Round(0).Unix()

		oldmain := os.Args[2]

		// Generate Keypair
		privateKey, publicKey := dasvid.IDKeyPair(os.Args[3])

		// check soucerpublickey vs audience
		parts := strings.Split(oldmain, ".")
		decodedpart, _ := base64.RawURLEncoding.DecodeString(parts[0])
		var tmpkey map[string]interface{}
		json.Unmarshal(decodedpart, &tmpkey)
		if (pubkey2string(publicKey) != tmpkey["aud"]) {
			fmt.Println("Incorrect append key!")
			os.Exit(1)
		}	

		// Generate next Keypair
		_, nextpublicKey := dasvid.IDKeyPair(os.Args[4])
		
		tokenclaims := map[string]interface{}{
			"iss":		pubkey2string(publicKey),
			"iat":	 	issue_time,
			"aud":		pubkey2string(nextpublicKey),
		}
		assertion, err := newschnorrencode(tokenclaims, oldmain, privateKey)
		if err != nil {
			fmt.Println("Error generating signed assertion!")
			os.Exit(1)
		} 

		fmt.Println("Generated assertion: ", fmt.Sprintf("%s",assertion))
		os.Exit(1)

	case "tracever":
		// 	Verify assertion signatures and iss/aud links
		//  usage: ./assertgen tracever assertion

		assertion := os.Args[2]

		validateschnorrtrace(assertion)
		os.Exit(1)

	case "schver":
		// 	Verify assertion signatures only
		//  usage: ./assertgen schver assertion

		assertion := os.Args[2]

		validateschnorrassertion(assertion)
		os.Exit(1)


	case "schapp":
		// Append an assertion with schnorr signature, using a new random keypair
		// usage: ./main schapp originaltoken assertionKey assertionValue

		// Generate Keypair
		privateKey, publicKey := dasvid.RandomKeyPair()

		// Issuer
		issuer := pubkey2string(publicKey)

		// Generate next Keypair
		nextprivateKey, nextpublicKey := dasvid.RandomKeyPair()

		// Audience
		audience := pubkey2string(nextpublicKey)		

		// timestamp
		issue_time 		:= time.Now().Round(0).Unix()

		// Original token
		oldmain 		:= os.Args[2]

		// assertion key:value
		assertionkey 	:= os.Args[3]
		assertionvalue 	:= os.Args[4]
		assertionclaims := map[string]interface{}{
			"iss"		:		issuer,
			"aud"		:	 	audience,
			"iat"		:	 	issue_time,
			assertionkey:		assertionvalue,
		}
		assertion, err := newschnorrencode(assertionclaims, oldmain, privateKey)
		if err != nil {
			fmt.Println("Error generating signed schnorr assertion!")
			os.Exit(1)
		} 

		fmt.Println("Generated assertion: ", fmt.Sprintf("%s",assertion))
		fmt.Println("Next private key   : ", nextprivateKey.String())

		os.Exit(1)
	
	case "concatenate":
		// Append an assertion with schnorr signature, using previous signature.S as key
		// usage: ./main concatenate_draft originaltoken assertionKey assertionValue

		// timestamp
		issue_time 		:= time.Now().Round(0).Unix()

		// Original token
		oldmain 		:= os.Args[2]
		parts 			:= strings.Split(oldmain, ".")
		fmt.Printf("Previous token: %s\n",  oldmain)
		tmpsig, _ 		:= base64.RawURLEncoding.DecodeString(parts[1])
		
		var origsignature dasvid.Signature
		buf := bytes.NewBuffer(tmpsig)
		if err := curve.Read(buf, &origsignature); err != nil {
			fmt.Printf("Error! value: %s\n",  err)
			os.Exit(1)
		}
		privateKey := origsignature.S
		publicKey := curve.Point().Mul(privateKey, curve.Point().Base())
		
		// Issuer
		issuer := pubkey2string(publicKey)
		
		// assertion key:value
		assertionkey 	:= os.Args[3]
		assertionvalue 	:= os.Args[4]
		assertionclaims := map[string]interface{}{
			"iss"		:		issuer,
			"iat"		:	 	issue_time,
			assertionkey:		assertionvalue,
		}
		assertion, err := newschnorrencode(assertionclaims, oldmain, privateKey)
		if err != nil {
			fmt.Println("Error generating signed schnorr assertion!")
			os.Exit(1)
		} 

		fmt.Println("Generated assertion: ", fmt.Sprintf("%s",assertion))

		os.Exit(1)
		
	case "ggschnorr_draft":
		// 	Verify assertion signature using Galindo Garcia
		//  usage: ./assertgen schver assertion

		assertion := os.Args[2]

		validategg(assertion)
		os.Exit(1)
		
		}

		if endpoint != "" {

			r, err := client.Get(endpoint)
			if err != nil {
				log.Fatalf("Error connecting to %q: %v", serverURL, err)
			}

			defer r.Body.Close()
			body, err := ioutil.ReadAll(r.Body)
			if err != nil {
				log.Fatalf("Unable to read body: %v", err)
			}

			fmt.Printf("%s", body)
		}
}

// jwkEncode encodes public part of an RSA or ECDSA key into a JWK.
// The result is also suitable for creating a JWK thumbprint.
// https://tools.ietf.org/html/rfc7517
func jwkEncode(pub crypto.PublicKey) (string, error) {
	switch pub := pub.(type) {
	case *rsa.PublicKey:
		// https://tools.ietf.org/html/rfc7518#section-6.3.1
		n := pub.N
		e := big.NewInt(int64(pub.E))
		// Field order is important.
		// See https://tools.ietf.org/html/rfc7638#section-3.3 for details.
		return fmt.Sprintf(`{"e":"%s","kty":"RSA","n":"%s"}`,
			base64.RawURLEncoding.EncodeToString(e.Bytes()),
			base64.RawURLEncoding.EncodeToString(n.Bytes()),
		), nil
	case *ecdsa.PublicKey:
		// https://tools.ietf.org/html/rfc7518#section-6.2.1
		p := pub.Curve.Params()
		n := p.BitSize / 8
		if p.BitSize%8 != 0 {
			n++
		}
		x := pub.X.Bytes()
		if n > len(x) {
			x = append(make([]byte, n-len(x)), x...)
		}
		y := pub.Y.Bytes()
		if n > len(y) {
			y = append(make([]byte, n-len(y)), y...)
		}
		// Field order is important.
		// See https://tools.ietf.org/html/rfc7638#section-3.3 for details.
		return fmt.Sprintf(`{"crv":"%s","kty":"EC","x":"%s","y":"%s"}`,
			p.Name,
			base64.RawURLEncoding.EncodeToString(x),
			base64.RawURLEncoding.EncodeToString(y),
		), nil
	}
	return "", nil
}

func newencode(claimset map[string]interface{}, oldmain string, key crypto.Signer) (string, error) {
	defer timeTrack(time.Now(), "newencode")

	//  Marshall received claimset into JSON
	cs, _ := json.Marshal(claimset)
	payload := base64.RawURLEncoding.EncodeToString(cs)

	// If no oldmain, generates a simple assertion
	if oldmain == "" {
		hash 	:= sha256.Sum256([]byte(payload))
		s, err 	:= ecdsa.SignASN1(rand.Reader, key.(*ecdsa.PrivateKey), hash[:])
		if err 	!= nil {
			fmt.Printf("Error signing: %s\n", err)
			return "", err
		}
		sig := base64.RawURLEncoding.EncodeToString(s)
		encoded := strings.Join([]string{payload, sig}, ".")

		fmt.Printf("Assertion size: %d\n", len(payload) + len(sig))

		return encoded, nil
	}
	
	//  Otherwise, append assertion to previous content (oldmain) and sign it
	hash	:= sha256.Sum256([]byte(payload + "." + oldmain))
	s, err 	:= ecdsa.SignASN1(rand.Reader, key.(*ecdsa.PrivateKey), hash[:])
	if err != nil {
		fmt.Printf("Error signing: %s\n", err)
		return "", err
	}
	signature := base64.RawURLEncoding.EncodeToString(s)
	encoded := strings.Join([]string{payload, oldmain, signature}, ".")
	
	fmt.Printf("Assertion size: %d\n", len(payload) + len(oldmain)+ len(signature))

	return encoded, nil
}

// Function to perform ecdsa token validation from out level to inside (last -> first assertion)
func validateassertion(token string) bool {
	defer timeTrack(time.Now(), "Validateassertion")

	parts := strings.Split(token, ".")

	//  Verify recursively all lvls except most inner
	var i = 0
	var j = len(parts)-1
	for (i < len(parts)/2 && (i+1 < j-1)) {
		// Extract first payload (parts[i]) and last signature (parts[j])
		clean 			:= strings.Join(strings.Fields(strings.Trim(fmt.Sprintf("%s", parts[i+1:j]), "[]")), ".")
		hash 			:= sha256.Sum256([]byte(parts[i] + "." + clean))
		signature, err 	:= base64.RawURLEncoding.DecodeString(parts[j])
		if err != nil {
			fmt.Printf("Error decoding signature: %s\n", err)
			return false
		}

		// retrieve key from IdP
		keys, err := getkeys(parts[i])
		if err != nil {
			fmt.Printf("Error decoding signature: %s\n", err)
			return false
		}

		fmt.Printf("Claim     %d: %s\n", i, parts[i])
		fmt.Printf("Signature %d: %s\n", j, parts[j])

		// Search for a valid key
		var z = 0
		for (z < len(keys)-1) {
			cleankeys 		:= strings.Trim(fmt.Sprintf("%s", keys[z]), "\\")
			
			var tmpkey map[string]interface{}
			json.Unmarshal([]byte(cleankeys), &tmpkey)
			pkey, _ 		:= base64.RawURLEncoding.DecodeString(fmt.Sprintf("%s", tmpkey["Pkey"]))
			finallykey, _ 	:= ParseECDSAPublicKey(fmt.Sprintf("%s", pkey))

			verify 			:= ecdsa.VerifyASN1(finallykey.(*ecdsa.PublicKey), hash[:], signature)
			if (verify == true){
				fmt.Printf("Signature successfully validated!\n\n")
				z = len(keys)-1
			} else {
				fmt.Printf("\nSignature validation failed!\n\n")
				if (z == len(keys)-2) {
					fmt.Printf("\nSignature validation failed! No keys remaining!\n\n")
					return false
				}
			}
			z++
		}
		i++
		j--
	}

	// Verify Inner lvl

	// Verify if signature j is valid to parts[i] (there is no remaining previous assertion)
	hash 			:= sha256.Sum256([]byte(parts[i]))
	signature, err 	:= base64.RawURLEncoding.DecodeString(parts[j])
	if (err != nil){
		fmt.Printf("Error decoding signature: %s\n", err)
		return false
	}

	// retrieve key from IdP
	keys, err := getkeys(parts[i])
	if err != nil {
		fmt.Printf("Error decoding signature: %s\n", err)
		return false
	}

	// fmt.Printf("Received Keys: %s\n", keys)
	fmt.Printf("Claim     %d: %s\n", i, parts[i])
	fmt.Printf("Signature %d: %s\n", j, parts[j])

	// verify if any of the received keys is valid
	var z = 0
	for (z < len(keys)-1) {
		cleankeys 		:= strings.Trim(fmt.Sprintf("%s", keys[z]), "\\")

		var lastkey map[string]interface{}
		json.Unmarshal([]byte(cleankeys), &lastkey)
		fmt.Printf("Search kid: %s\n", lastkey["Kid"])
		key, _ 			:= base64.RawURLEncoding.DecodeString(fmt.Sprintf("%s", lastkey["Pkey"]))
		finallykey, _ 	:= ParseECDSAPublicKey(fmt.Sprintf("%s", key))
		
		verify := ecdsa.VerifyASN1(finallykey.(*ecdsa.PublicKey), hash[:], signature)
		if (verify == true){
			fmt.Printf("Signature successfully validated!\n\n")
			z = len(keys)-1
		} else {
			fmt.Printf("\nSignature validation failed!\n\n")
			if (z == len(keys)-2) {
				fmt.Printf("\nSignature validation failed! No keys remaining!\n\n")
				return false
			}
		}
		z++
	}
	return true
}

// Function to perform schnorr token validation from out level to inside (last -> first assertion)
// This function did not check iss/aud link
func validateschnorrassertion(token string) bool {
	defer timeTrack(time.Now(), "Validateassertion")

	parts := strings.Split(token, ".")

	//  Verify recursively all lvls except most inner
	var i = 0
	var j = len(parts)-1
	for (i < len(parts)/2 && (i+1 < j-1)) {
		// Extract first payload (parts[i]) and last signature (parts[j])
		clean 	:= strings.Join(strings.Fields(strings.Trim(fmt.Sprintf("%s", parts[i+1:j]), "[]")), ".")
		message := strings.Join([]string{parts[i], clean}, ".")
		
		// Load kyber.Signature from token
		signature := loadsig(parts[j])

		// // verify aud/iss link
		// link := checkaudlink(parts[i], parts[i+1])
		// if (link == false) {
		// 	return false
		// }	

		// extract publickey (kyber.Point) from issuer claim
		pubkey := string2pubkey(parts[i])
		fmt.Printf("Retrieved PublicKey from token: %s\n", pubkey.String())

    	fmt.Printf("Signature verification: %t\n\n", dasvid.Verify(message, signature, pubkey))
		i++
		j--
	}

	// Verify Inner lvl
	message := parts[i]
		
	// Load kyber.Signature from token
	signature := loadsig(parts[j])

	// extract publickey (kyber.Point) from issuer claim
	pubkey := string2pubkey(parts[i])
	fmt.Printf("Retrieved PublicKey from token: %s\n", pubkey.String())

	// Verify signature using extracted public key
	sigresult := dasvid.Verify(message, signature, pubkey)

   	fmt.Printf("Signature verification: %t\n\n", sigresult)
	return sigresult
}

// validateschnorrtrace include iss/aud link validation
func validateschnorrtrace(token string) bool {
	defer timeTrack(time.Now(), "Validateassertion")

	parts := strings.Split(token, ".")

	//  Verify recursively all lvls except most inner
	var i = 0
	var j = len(parts)-1
	for (i < len(parts)/2 && (i+1 < j-1)) {
		// Extract first payload (parts[i]) and last signature (parts[j])
		clean 	:= strings.Join(strings.Fields(strings.Trim(fmt.Sprintf("%s", parts[i+1:j]), "[]")), ".")
		message := strings.Join([]string{parts[i], clean}, ".")
		
		// Load kyber.Signature from token
		signature := loadsig(parts[j])

		// verify aud/iss link
		link := checkaudlink(parts[i], parts[i+1])
		if (link == false) {
			return false
		}	

		// extract publickey (kyber.Point) from issuer claim
		pubkey := string2pubkey(parts[i])
		fmt.Printf("Retrieved PublicKey from token: %s\n", pubkey.String())

    	fmt.Printf("Signature verification: %t\n\n", dasvid.Verify(message, signature, pubkey))
		i++
		j--
	}

	// Verify Inner lvl
	message := parts[i]
		
	// Load kyber.Signature from token
	signature := loadsig(parts[j])

	// extract publickey (kyber.Point) from issuer claim
	pubkey := string2pubkey(parts[i])
	fmt.Printf("Retrieved PublicKey from token: %s\n", pubkey.String())

	// Verify signature using extracted public key
	sigresult := dasvid.Verify(message, signature, pubkey)

   	fmt.Printf("Signature verification: %t\n\n", sigresult)
	return sigresult
}

func validategg(token string) bool {
	defer timeTrack(time.Now(), "validategg")

	// UNDER DEVELOPMENT. NOT WORKING

	parts := strings.Split(token, ".")

	// First implementation aims to successfully execute galindo-garcia signature verification (2 hops only)
	// Calculate h0 and h1 and retrieve r0 and r1

	// r0 and h0
	message 		:= parts[1]
	// // Retrieve public key
	// decodedmsg, _ := base64.RawURLEncoding.DecodeString(message)
	// fmt.Printf("decodedparti value: %s\n",  decodedmsg)
	// var tmpmsg map[string]interface{}
	// json.Unmarshal([]byte(decodedmsg), &tmpmsg)
	// tmppubkey, _ := base64.RawURLEncoding.DecodeString(fmt.Sprintf("%s", tmpmsg["iss"]))
	// var pubkey0 kyber.Point
	// buf = bytes.NewBuffer(tmppubkey)
	// if err := curve.Read(buf, &pubkey0); err != nil {
	// 	fmt.Printf("Error! value: %s\n",  err)
	// 	os.Exit(1)
	// }
	// fmt.Printf("derived PublicKey: %s\n", pubkey0.String())

	tmpsig, err 	:= base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		fmt.Printf("Error decoding signature: %s\n", err)
		return false
	}
	var signature dasvid.Signature
	buf := bytes.NewBuffer(tmpsig)
	if err := curve.Read(buf, &signature); err != nil {
		fmt.Printf("Error! value: %s\n",  err)
		os.Exit(1)
	}
	// r0 := signature.R
	// TODO: ainda falta inserir a pubkey, como eu fiz no sign e verify. Senão vai falhar.
	// tmp := pubkey0.String()+signature.R.String()+message
	// h0 := dasvid.Hash(tmp)

	// r1 and h1
	message1		:= parts[0]
	// // Retrieve public key
	// decodedmsg1, _ := base64.RawURLEncoding.DecodeString(message1)
	// fmt.Printf("decodedparti value: %s\n",  decodedmsg1)
	// var tmpmsg1 map[string]interface{}
	// json.Unmarshal([]byte(decodedmsg), &tmpmsg1)
	// tmppubkey, _ = base64.RawURLEncoding.DecodeString(fmt.Sprintf("%s", tmpmsg1["iss"]))
	// var pubkey1 kyber.Point
	// buf = bytes.NewBuffer(tmppubkey)
	// if err := curve.Read(buf, &pubkey1); err != nil {
	// 	fmt.Printf("Error! value: %s\n",  err)
	// 	os.Exit(1)
	// }
	// fmt.Printf("derived PublicKey: %s\n", pubkey1.String())

	tmpsig1, err 	:= base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		fmt.Printf("Error decoding signature: %s\n", err)
		return false
	}
	var signature1 dasvid.Signature
	buf1 := bytes.NewBuffer(tmpsig1)
	if err := curve.Read(buf1, &signature1); err != nil {
		fmt.Printf("Error! value: %s\n",  err)
		os.Exit(1)
	}
	fmt.Printf("Received signature: %s\n", signature1)
	// r1 := signature1.R
	// TODO: ainda falta inserir a pubkey, como eu fiz no sign e verify. Senão vai falhar.
	// tmp1 := pubkey1.String()+signature1.R.String()+message1
	// h1 := dasvid.Hash(tmp1)	


	fmt.Printf("Signature verification: %t\n\n", dasvid.Verifygg(message, signature, message1, signature1))

	return true
}

func printtoken(token string) {

	// Split received token
	parts := strings.Split(token, ".")
	fmt.Println("Total parts: ", len(parts))
	if (len(parts) < 2) {
		fmt.Printf("Invalid number of parts!")
		os.Exit(1)
	}

	// print single assertion
	if (len(parts) < 3) {
		dectmp, _ := base64.RawURLEncoding.DecodeString(parts[0])
		fmt.Printf("Claim     [%d]	: %s\n", 0, dectmp)
		fmt.Printf("Signature [%d]	: %s\n", 1, parts[1])
		os.Exit(1)
	}
	
	// print token claims
	var i = 0
	for (i < len(parts)/2) {
		dectmp, _ := base64.RawURLEncoding.DecodeString(parts[i])
		fmt.Printf("Claim     [%d]	: %s\n", i, dectmp)
		i++
	}

	// print token  signatures
	j := len(parts)/2
	for ( j < len(parts)) {
		fmt.Printf("Signature [%d]	: %s\n", j, parts[j])
		j++
	}

}

func timeTrack(start time.Time, name string) {
    elapsed := time.Since(start)
    fmt.Printf("\n%s execution time is %s\n", name, elapsed)
}

func addkey(key string) (string, error) {

    // url := "http://"+filesrv+":"+filesrvport+"/addnft"
	url := "http://localhost:8888/addkey"
    fmt.Printf("\nKey Server URL: %s\n", url)

    var jsonStr = []byte(key)
    req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
    req.Header.Set("X-Custom-Header", "keydata")
    req.Header.Set("Content-Type", "application/json")

    client := &http.Client{}
    resp, err := client.Do(req)
    if err != nil {
        fmt.Errorf("error: %s", err)
        return "", err
    }
    defer resp.Body.Close()

    // fmt.Println("response Status:", resp.Status)
    // fmt.Println("response Headers:", resp.Header)
    body, _ := ioutil.ReadAll(resp.Body)
    // fmt.Println("response Body:", string(body))

	return string(body), nil
}

func getkeys(message string) ([]string, error) {

	decclaim, _ := base64.RawURLEncoding.DecodeString(message)
	var tmpkey map[string]interface{}
	json.Unmarshal([]byte(decclaim), &tmpkey)
	kid := tmpkey["kid"]
	fmt.Printf("Search kid: %s\n", kid)

	url := "http://localhost:8888/key/" + fmt.Sprintf("%s", kid)
    fmt.Printf("\nKey Server URL: %s\n", url)

    var jsonStr = []byte(fmt.Sprintf("%s", kid))
    req, err := http.NewRequest("GET", url, bytes.NewBuffer(jsonStr))

    client := &http.Client{}
    resp, err := client.Do(req)
    if err != nil {
        fmt.Errorf("error: %s", err)
        return nil, err
    }
    defer resp.Body.Close()

    body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
        fmt.Errorf("error: %s", err)
        return nil, err
    }

	keys := strings.SplitAfter(fmt.Sprintf("%s", string(body)), "}")
	fmt.Printf("Number of Keys received from IdP: %d\n\n", len(keys)-1)
	if (len(keys)-1 == 0){
		fmt.Printf("\nError: No keys received!\n\n")
		return  nil, err
	}

	return keys, nil

}

// EncodeECDSAPublicKey encodes an *ecdsa.PublicKey to PEM format.
//  TODO: FIX type, that should be different based on input key type
// At this time it only support ECDSA
func EncodeECDSAPublicKey(key *ecdsa.PublicKey) ([]byte, error) {

	derKey, err := x509.MarshalPKIXPublicKey(key)
		if err != nil {
			return nil, err
		}

	keyBlock := &pem.Block{
		Type:  "EC PUBLIC KEY",
		Bytes: derKey,
	}

	return pem.EncodeToMemory(keyBlock), nil
}

func ParseECDSAPublicKey(pubPEM string) (interface{}, error){
	block, _ := pem.Decode([]byte(pubPEM))
	if block == nil {
		panic("failed to parse PEM block containing the public key")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		panic("failed to parse DER encoded public key: " + err.Error())
	}

	// switch pub := pub.(type) {
	// case *rsa.PublicKey:
	// 	fmt.Println("pub is of type RSA:", pub)
	// // case *dsa.PublicKey:
	// // 	fmt.Println("pub is of type DSA:", pub)
	// case *ecdsa.PublicKey:
	// 	fmt.Println("pub is of type ECDSA:", pub)
	// // case ed25519.PublicKey:
	// // 	fmt.Println("pub is of type Ed25519:", pub)
	// default:
	// 	panic("unknown type of public key")
	// }
	
	return pub,nil

}

func newschnorrencode(claimset map[string]interface{}, oldmain string, key kyber.Scalar) (string, error) {
	defer timeTrack(time.Now(), "newencode")

	//  Marshall received claimset into JSON
	cs, _ := json.Marshal(claimset)
	payload := base64.RawURLEncoding.EncodeToString(cs)
		
	// If no oldmain, generates a simple assertion...
	if oldmain == "" {
		tmpsig := dasvid.Sign(payload, key)
		// fmt.Printf("Generated Signature: %s\n", tmpsig.String())

		sigbuf := bytes.Buffer{}
		if err :=  curve.Write(&sigbuf, &tmpsig); err != nil {
			fmt.Printf("Error! value: %s\n",  err)
			os.Exit(1)
		}
		signature := base64.RawURLEncoding.EncodeToString(sigbuf.Bytes())

		encoded := strings.Join([]string{payload, signature}, ".")

		// debug
		fmt.Printf("message size in base64 : %d\n", len(payload))
		fmt.Printf("sig size in base64     : %d\n", len(signature))
		fmt.Printf("Assertion size         : %d\n", len(payload) + len(signature))

		return encoded, nil
	}
	

	//  ...otherwise, append assertion to previous content (oldmain) and sign all
	message := strings.Join([]string{payload, oldmain}, ".")
	tmpsig := dasvid.Sign(message, key)
	// fmt.Printf("Generated Signature: %s\n", tmpsig.String())
	buf := bytes.Buffer{}
	if err :=  curve.Write(&buf, &tmpsig); err != nil {
		fmt.Printf("Error! value: %s\n",  err)
		os.Exit(1)
	}
	signature := base64.RawURLEncoding.EncodeToString(buf.Bytes())

	encoded := strings.Join([]string{message, signature}, ".")

	// debug
	fmt.Printf("message size in base64 : %d\n", len(message))
	fmt.Printf("sig size in base64     : %d\n", len(signature))
	fmt.Printf("Assertion size         : %d\n", len(message) + len(signature))

	return encoded, nil
}

func pubkey2string(publicKey kyber.Point) string {
	buf := bytes.Buffer{}
	if err :=  curve.Write(&buf, &publicKey); err != nil {
		fmt.Printf("Error! value: %s\n",  err)
		os.Exit(1)
	}
	result := base64.RawURLEncoding.EncodeToString(buf.Bytes())
	return result
}

func string2pubkey(message string) kyber.Point {

	// Decode from b64 and retrieve issuer claim (public key)
	decodedparti, _ := base64.RawURLEncoding.DecodeString(message)
	fmt.Printf("message value: %s\n",  decodedparti)
	var tmp map[string]interface{}
	json.Unmarshal([]byte(decodedparti), &tmp)
	tmppubkey, _ := base64.RawURLEncoding.DecodeString(fmt.Sprintf("%s", tmp["iss"]))

	// Convert claim to curve point
	var pubkey kyber.Point
	buf := bytes.NewBuffer(tmppubkey)
	if err := curve.Read(buf, &pubkey); err != nil {
		fmt.Printf("Error! value: %s\n",  err)
		os.Exit(1)
	}

	return pubkey
}

func checkaudlink(issmsg string, audmsg string) bool {

	// Decode issmsg from b64 and retrieve issuer claim 
	decodediss, _ := base64.RawURLEncoding.DecodeString(issmsg)
	// fmt.Printf("Issuer message value: %s\n",  decodediss)
	var tmpiss map[string]interface{}
	json.Unmarshal([]byte(decodediss), &tmpiss)

	// Decode audmsg from b64 and retrieve audience claim 
	decodedaud, _ := base64.RawURLEncoding.DecodeString(audmsg)
	// fmt.Printf("Audience message value: %s\n",  decodediss)
	var tmpaud map[string]interface{}
	json.Unmarshal([]byte(decodedaud), &tmpaud)

	// check if iss == aud
	if (tmpiss["iss"] != tmpaud["aud"]) {
		fmt.Printf("\nIssuer/Audience link fails!\n")
		return false
	}
	fmt.Printf("\nIssuer/Audience link validated!\n")
	return true
}

func loadsig(sig string) dasvid.Signature {

	tmpsig, err 	:= base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		fmt.Printf("Error decoding signature: %s\n", err)
		os.Exit(1)
	} 
	var signature dasvid.Signature
	buf := bytes.NewBuffer(tmpsig)
	if err := curve.Read(buf, &signature); err != nil {
		fmt.Printf("Error loading token signature to curve! Error: %s\n",  err)
		os.Exit(1)
	}

	return signature
}

func ParseEnvironment() {

	if _, err := os.Stat(".cfg"); os.IsNotExist(err) {
		log.Printf("Config file (.cfg) is not present.  Relying on Global Environment Variables")
	}

	setEnvVariable("SOCKET_PATH", os.Getenv("SOCKET_PATH"))
	if os.Getenv("SOCKET_PATH") == "" {
		log.Printf("Could not resolve a SOCKET_PATH environment variable.")
		// os.Exit(1)
	}
	
}

func setEnvVariable(env string, current string) {
	if current != "" {
		return
	}

	file, _ := os.Open(".cfg")
	defer file.Close()

	lookInFile := bufio.NewScanner(file)
	lookInFile.Split(bufio.ScanLines)

	for lookInFile.Scan() {
		parts := strings.Split(lookInFile.Text(), "=")
		key, value := parts[0], parts[1]
		if key == env {
			os.Setenv(key, value)
		}
	}
}

// ----------- DRAFT -------------------

// Function to perform token validation from inner level to outside (first -> last assertion)
// TODO: should be necessary to receive array of keys to validate each level with its correspondent key
// 		Other possibility is the function call the directory service to retrieve the key, inside for
// 		Since keyserver is running, its necessary to use here to retrieve correct keys (similar to validate function)
// func validatreverse(token string, pubkey *ecdsa.PublicKey) bool {
// 	defer timeTrack(time.Now(), "Validatreverse")

// 	parts := strings.Split(token, ".")

// 	//  Verify recursively all lvls except most inner
// 	var i = (len(parts)/2)-1
// 	var j = (len(parts)/2)
// 	for (i >= 0) {
// 		fmt.Printf("\nClaim     %d: %s\n", i, parts[i])
// 		fmt.Printf("Signature %d: %s\n", j,  parts[j])

// 		// Extract first payload (parts[i]) and last signature (parts[j])
// 		clean := strings.Join(strings.Fields(strings.Trim(fmt.Sprintf("%s", parts[i+1:j]), "[]")), ".")
// 		var hash [32]byte
// 		if (clean != "") {
// 			hash = sha256.Sum256([]byte(parts[i] + "." + clean))
// 		} else {
// 			hash = sha256.Sum256([]byte(parts[i]))
// 		}

// 		signature, err := base64.RawURLEncoding.DecodeString(parts[j])
// 		if err != nil {
// 			return false
// 		}

// 		// Verify if signature j is valid to payload + previous assertion (parts[i+1:j])
// 		verify := ecdsa.VerifyASN1(pubkey, hash[:], signature)
// 		if (verify == true)	{
// 			fmt.Printf("Signature successfully validated!\n\n")
// 		} else {
// 			fmt.Printf("Signature validation failed!\n\n")
// 			return false
// 		}
// 		i--
// 		j++
// 	}
// 	return true
// }