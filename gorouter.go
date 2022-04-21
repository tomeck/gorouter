package main

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	//	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Transaction struct {
	Id        primitive.ObjectID `json:"id,omitempty"`
	ApiKey    string             `json:"apikey,omitempty" validate:"required"`
	TestRunId string             `json:"testrunid,omitempty" validate:"required"`
	Status    int                `json:"status,omitempty" validate:"required"`
	Url       string             `json:"url,omitempty" validate:"required"`
	Headers   string             `json:"headers,omitempty" validate:"required"`
	Request   string             `json:"request,omitempty" validate:"required"`
	Response  string             `json:"response,omitempty" validate:"required"`
	Timestamp time.Time          `json:"timestamp,omitempty" validate:"required"`
}

// Connect to database; get client, context and CancelFunc back
func connect(uri string) (*mongo.Client, context.Context, context.CancelFunc, error) {

	// ctx will be used to set deadline for process, here
	// deadline will of 30 seconds.
	ctx, cancel := context.WithTimeout(context.Background(),
		30*time.Second)

	// mongo.Connect return mongo.Client method
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	return client, ctx, cancel, err
}

// Closes mongoDB connection and cancel context.
func close(client *mongo.Client, ctx context.Context,
	cancel context.CancelFunc) {

	// CancelFunc to cancel to context
	defer cancel()

	// client provides a method to close
	// a mongoDB connection.
	defer func() {

		// client.Disconnect method also has deadline.
		// returns error if any,
		if err := client.Disconnect(ctx); err != nil {
			panic(err)
		}
	}()
}

func stringifyHeaders(headers http.Header) string {

	returnVal := ""

	// Loop over header names
	for name, values := range headers {
		// Loop over all values for the name.
		for _, value := range values {
			returnVal += name + ":" + value + ","
		}
	}

	return returnVal
}

func recordTransaction(headers http.Header, Url string, status int, request []byte, response []byte) error {

	tx := Transaction{
		Id:        primitive.NewObjectID(),
		ApiKey:    headers.Get("Api-Key"),
		TestRunId: headers.Get("X-TESTRUN-ID"),
		Status:    status,
		Url:       Url,
		Headers:   stringifyHeaders(headers),
		Request:   string(request),
		Response:  string(response),
		Timestamp: time.Now(),
	}

	result, err := txCollection.InsertOne(ctx, tx)
	if err != nil {
		fmt.Println("Error inserting document", err)
	} else {
		fmt.Println("Inserted document", result)
	}

	return nil
}

/*
// This gets called when the response comes back from origin server
// We write the request and response to database
func rewriteBody(resp *http.Response) (err error) {

	req := resp.Request
	fmt.Println("Request %v", req)

	// Get original request from response
	requestBytes, err := ioutil.ReadAll(req.Body)
	if err != nil {
		return err
	}
	fmt.Println("Request bytes %v", requestBytes)

	// Read response from origin server
	responseBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	err = resp.Body.Close()
	if err != nil {
		return err
	}

	// Rewrite body so client receives origin server response
	body := ioutil.NopCloser(bytes.NewReader(responseBytes))
	resp.Body = body

	return nil
}
*/

type myTransport struct {
	// Uncomment this if you want to capture the transport
	// CapturedTransport http.RoundTripper
}

func (t *myTransport) RoundTrip(request *http.Request) (*http.Response, error) {

	// Get original request from response
	Url := request.URL.Path

	requestBytes, err := ioutil.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	// fmt.Println("Request", requestBytes)

	// Rewrite body so client receives origin server response
	body := ioutil.NopCloser(bytes.NewReader(requestBytes))
	request.Body = body

	// Perform the roundtrip call
	response, err := http.DefaultTransport.RoundTrip(request)

	status := response.StatusCode

	// Get response
	responseBytes, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	// fmt.Println("responseBytes bytes %v", responseBytes)

	// Rewrite body so client receives origin server response
	body = ioutil.NopCloser(bytes.NewReader(responseBytes))
	response.Body = body

	recordTransaction(request.Header, Url, status, requestBytes, responseBytes)

	return response, err
}

// NewProxy takes target host and creates a reverse proxy
func NewProxy(targetHost string) (*httputil.ReverseProxy, error) {
	url, err := url.Parse(targetHost)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(url)

	prevDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		fmt.Printf("Proxying request for URL %s\n", req.URL)

		/*
			//*******
			// JTE TEST : read headers
			// Loop over header names
			for name, values := range req.Header {
				// Loop over all values for the name.
				for _, value := range values {
					fmt.Println(name, value)
				}
			}
			//*******

			//*******
			// JTE TEST : read body
			body, err := ioutil.ReadAll(req.Body)
			if err != nil {
				log.Printf("Error reading body: %v", err)
				//http.Error(w, "can't read body", http.StatusBadRequest)
				return
			}

			// Work / inspect body. You may even modify it!
			fmt.Println(body)
			//recordTransaction(req.Header, body)

			// And now set a new body, which will simulate the same data we read:
			req.Body = ioutil.NopCloser(bytes.NewBuffer(body))
			//*******
		*/

		prevDirector(req)

		req.Header.Add("X-Forwarded-Host", req.Host)
		req.Header.Add("X-Origin-Host", url.Host)
		req.URL.Scheme = url.Scheme
		req.Host = req.URL.Host
		req.URL.Host = url.Host

		req.Close = true
	}

	//proxy.ModifyResponse = rewriteBody
	proxy.Transport = &myTransport{}

	return proxy, nil
}

// ProxyRequestHandler handles the http request using proxy
func ProxyRequestHandler(proxy *httputil.ReverseProxy) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	}
}

// Constants
const CHARGES_BASE_URL = "https://cert.api.fiservapps.com/"

// Globals
// JTE TODO is there something better to do with these?
var client *mongo.Client
var ctx context.Context
var cancel context.CancelFunc
var db *mongo.Database
var txCollection *mongo.Collection

func main() {
	// initialize a reverse proxy and pass the actual backend server url here
	proxy, err := NewProxy(CHARGES_BASE_URL) // ("http://localhost:8081")
	if err != nil {
		panic(err)
	}

	// Initialize database (hardcoded for local machine)
	client, ctx, cancel, err := connect("mongodb://localhost:27017")
	if err != nil {
		panic(err)
	}
	fmt.Println("Connected to local mongodb")

	// Get target database and collection
	db = client.Database("dstest")
	txCollection = db.Collection("transactions")
	fmt.Println("Initialized db and collection")

	// Close db when the main function is returned.
	defer close(client, ctx, cancel)

	// handle all requests to your server using the proxy
	http.HandleFunc("/", ProxyRequestHandler(proxy))
	log.Fatal(http.ListenAndServe(":8080", nil))
}
