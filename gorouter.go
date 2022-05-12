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
	"os"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Constants
const CHARGES_BASE_URL = "https://cert.api.fiservapps.com/"

// const DB_CONNECTION_STRING = "mongodb://localhost:27017"
const DB_CONNECTION_STRING = "mongodb+srv://admin:Ngokman3#@cluster0.mce8u.mongodb.net/dstest?retryWrites=true&w=majority"

// Globals
// JTE TODO is there something better to do with these globals?
var client *mongo.Client
var ctx context.Context
var cancel context.CancelFunc
var db *mongo.Database
var txCollection *mongo.Collection

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

		prevDirector(req)

		req.Header.Add("X-Forwarded-Host", req.Host)
		req.Header.Add("X-Origin-Host", url.Host)
		req.URL.Scheme = url.Scheme
		req.Host = req.URL.Host
		req.URL.Host = url.Host

		req.Close = true
	}

	proxy.Transport = &myTransport{}

	return proxy, nil
}

// ProxyRequestHandler handles the http request using proxy
func ProxyRequestHandler(proxy *httputil.ReverseProxy) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	}
}

func main() {
	// initialize a reverse proxy and pass the actual backend server url here
	proxy, err := NewProxy(CHARGES_BASE_URL)
	if err != nil {
		panic(err)
	}

	// Initialize database (hardcoded for local machine)
	// client, ctx, cancel, err := connect("mongodb://localhost:27017")
	// if err != nil {
	// 	fmt.Println(err)
	// 	panic(err)
	// }
	// fmt.Println("Connected to local mongodb")
	serverAPIOptions := options.ServerAPI(options.ServerAPIVersion1)
	clientOptions := options.Client().
		ApplyURI(DB_CONNECTION_STRING).
		SetServerAPIOptions(serverAPIOptions)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("x3Connected to mongo at", DB_CONNECTION_STRING)

	// Get target database and collection
	db = client.Database("dstest")
	txCollection = db.Collection("transactions")
	fmt.Println("Initialized db and collection")

	// Close db when the main function is returned.
	defer close(client, ctx, cancel)

	// handle all requests to your server using the proxy
	// Determine port for HTTP service.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		fmt.Printf("defaulting to port %s\n", port)
	}

	fmt.Printf("Listening on port %s\n", port)

	http.HandleFunc("/", ProxyRequestHandler(proxy))
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
