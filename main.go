package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// Event representa un evento en el sistema
type Event struct {
	ID    int    `json:"id"`
	Value string `json:"value"`
}

// State representa el estado replicado del nodo
type State struct {
	SequenceNumber int     `json:"sequence_number"`
	EventLog       []Event `json:"event_log"`
}

// Node representa la configuración de un nodo
type Node struct {
	ID   int    `json:"id"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

// Config representa la configuración del sistema
type Config struct {
	Nodes []Node `json:"nodes"`
}

// ServerNode representa un nodo del sistema distribuido
type ServerNode struct {
	config          Config
	nodeID          int
	host            string
	port            int
	state           State
	stateMutex      sync.RWMutex
	isPrimary       bool
	primaryID       int
	primaryMutex    sync.RWMutex
	httpServer      *http.Server
	ctx             context.Context
	cancel          context.CancelFunc
	heartbeatTicker *time.Ticker
	stopChan        chan bool
}

// NewServerNode crea una nueva instancia de ServerNode
func NewServerNode(configPath string, nodeID int) (*ServerNode, error) {
	// Leer configuración
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("error leyendo config.json: %v", err)
	}

	var config Config
	if err := json.Unmarshal(configData, &config); err != nil {
		return nil, fmt.Errorf("error parseando config.json: %v", err)
	}

	// Encontrar el nodo actual en la configuración
	var currentNode Node
	found := false
	for _, node := range config.Nodes {
		if node.ID == nodeID {
			currentNode = node
			found = true
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("nodo con ID %d no encontrado en la configuración", nodeID)
	}

	ctx, cancel := context.WithCancel(context.Background())

	node := &ServerNode{
		config:    config,
		nodeID:    nodeID,
		host:      currentNode.Host,
		port:      currentNode.Port,
		state:     State{SequenceNumber: 0, EventLog: []Event{}},
		isPrimary: false,
		primaryID: -1,
		ctx:       ctx,
		cancel:    cancel,
		stopChan:  make(chan bool),
	}

	// Cargar estado persistente si existe
	node.loadState()

	return node, nil
}

// loadState carga el estado desde el archivo persistente
func (sn *ServerNode) loadState() {
	filename := fmt.Sprintf("node_%d_state.json", sn.nodeID)
	data, err := os.ReadFile(filename)
	if err != nil {
		log.Printf("No se encontró archivo de estado previo para nodo %d, iniciando con estado vacío", sn.nodeID)
		return
	}

	sn.stateMutex.Lock()
	defer sn.stateMutex.Unlock()

	if err := json.Unmarshal(data, &sn.state); err != nil {
		log.Printf("Error parseando archivo de estado: %v", err)
	}
}

// saveState guarda el estado en el archivo persistente
func (sn *ServerNode) saveState() error {
	sn.stateMutex.RLock()
	stateCopy := sn.state
	sn.stateMutex.RUnlock()

	data, err := json.MarshalIndent(stateCopy, "", "  ")
	if err != nil {
		return fmt.Errorf("error serializando estado: %v", err)
	}

	filename := fmt.Sprintf("node_%d_state.json", sn.nodeID)
	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("error guardando estado: %v", err)
	}

	return nil
}

// getNodeURL obtiene la URL completa de un nodo
func (sn *ServerNode) getNodeURL(nodeID int) string {
	for _, node := range sn.config.Nodes {
		if node.ID == nodeID {
			return fmt.Sprintf("http://%s:%d", node.Host, node.Port)
		}
	}
	return ""
}

// Start inicia el nodo servidor
func (sn *ServerNode) Start() error {
	// Configurar rutas HTTP
	mux := http.NewServeMux()
	mux.HandleFunc("/heartbeat", sn.handleHeartbeat)
	mux.HandleFunc("/replicate", sn.handleReplicate)
	mux.HandleFunc("/election", sn.handleElection)
	mux.HandleFunc("/coordinator", sn.handleCoordinator)
	mux.HandleFunc("/sync", sn.handleSync)
	mux.HandleFunc("/event", sn.handleEvent)
	mux.HandleFunc("/status", sn.handleStatus)

	// Crear servidor HTTP
	sn.httpServer = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", sn.host, sn.port),
		Handler: mux,
	}

	// Iniciar servidor HTTP en goroutine
	go func() {
		log.Printf("Nodo %d iniciando en %s:%d", sn.nodeID, sn.host, sn.port)
		if err := sn.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Error en servidor HTTP: %v", err)
		}
	}()

	// Iniciar elección inicial
	go sn.startInitialElection()

	return nil
}

// startInitialElection inicia la elección inicial del sistema
func (sn *ServerNode) startInitialElection() {
	time.Sleep(2 * time.Second) // Esperar a que todos los nodos estén listos
	log.Printf("Nodo %d iniciando elección inicial", sn.nodeID)
	sn.startElection()
}

// startElection inicia el proceso de elección del matón
func (sn *ServerNode) startElection() {
	log.Printf("Nodo %d iniciando elección", sn.nodeID)

	// Encontrar nodos con ID superior
	var higherNodes []int
	for _, node := range sn.config.Nodes {
		if node.ID > sn.nodeID {
			higherNodes = append(higherNodes, node.ID)
		}
	}

	if len(higherNodes) == 0 {
		// Este nodo tiene el ID más alto, se convierte en primario
		log.Printf("Nodo %d se convierte en primario (ID más alto)", sn.nodeID)
		sn.becomePrimary()
		return
	}

	// Enviar mensajes de elección a nodos con ID superior
	electionStarted := false
	for _, higherNodeID := range higherNodes {
		if sn.sendElectionMessage(higherNodeID) {
			electionStarted = true
			break
		}
	}

	if !electionStarted {
		// No se pudo contactar a ningún nodo superior, este nodo se convierte en primario
		log.Printf("Nodo %d se convierte en primario (no hay nodos superiores disponibles)", sn.nodeID)
		sn.becomePrimary()
	}
}

// sendElectionMessage envía un mensaje de elección a un nodo específico
func (sn *ServerNode) sendElectionMessage(targetNodeID int) bool {
	url := sn.getNodeURL(targetNodeID) + "/election"

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		log.Printf("Nodo %d no pudo contactar al nodo %d: %v", sn.nodeID, targetNodeID, err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		log.Printf("Nodo %d recibió respuesta de elección del nodo %d", sn.nodeID, targetNodeID)
		return true
	}

	return false
}

// becomePrimary convierte este nodo en primario
func (sn *ServerNode) becomePrimary() {
	sn.primaryMutex.Lock()
	sn.isPrimary = true
	sn.primaryID = sn.nodeID
	sn.primaryMutex.Unlock()

	log.Printf("Nodo %d es ahora el PRIMARIO", sn.nodeID)

	// Anunciar victoria a todos los demás nodos
	go sn.announceVictory()

	// Iniciar monitoreo de nodos secundarios
	go sn.startSecondaryMonitoring()
}

// announceVictory anuncia la victoria de la elección a todos los demás nodos
func (sn *ServerNode) announceVictory() {
	for _, node := range sn.config.Nodes {
		if node.ID != sn.nodeID {
			go sn.sendCoordinatorMessage(node.ID)
		}
	}
}

// sendCoordinatorMessage envía mensaje de coordinador a un nodo específico
func (sn *ServerNode) sendCoordinatorMessage(targetNodeID int) {
	url := sn.getNodeURL(targetNodeID) + "/coordinator"

	coordinatorData := map[string]int{
		"primary_id": sn.nodeID,
	}

	jsonData, _ := json.Marshal(coordinatorData)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Error enviando mensaje de coordinador al nodo %d: %v", targetNodeID, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		log.Printf("Nodo %d confirmó coordinador %d", targetNodeID, sn.nodeID)
	}
}

// startSecondaryMonitoring inicia el monitoreo de nodos secundarios
func (sn *ServerNode) startSecondaryMonitoring() {
	// Como primario, no necesitamos monitorear a otros nodos
	// Los secundarios monitorean al primario
}

// startPrimaryMonitoring inicia el monitoreo del primario (solo para secundarios)
func (sn *ServerNode) startPrimaryMonitoring() {
	sn.heartbeatTicker = time.NewTicker(2 * time.Second)
	defer sn.heartbeatTicker.Stop()

	for {
		select {
		case <-sn.heartbeatTicker.C:
			sn.checkPrimaryHeartbeat()
		case <-sn.stopChan:
			return
		case <-sn.ctx.Done():
			return
		}
	}
}

// checkPrimaryHeartbeat verifica si el primario está vivo
func (sn *ServerNode) checkPrimaryHeartbeat() {
	sn.primaryMutex.RLock()
	primaryID := sn.primaryID
	sn.primaryMutex.RUnlock()

	if primaryID == -1 || primaryID == sn.nodeID {
		return // No hay primario o este nodo es el primario
	}

	url := sn.getNodeURL(primaryID) + "/heartbeat"

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		log.Printf("Nodo %d no pudo contactar al primario %d: %v", sn.nodeID, primaryID, err)
		sn.handlePrimaryFailure()
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Primario %d respondió con estado %d", primaryID, resp.StatusCode)
		sn.handlePrimaryFailure()
	}
}

// handlePrimaryFailure maneja la detección de fallo del primario
func (sn *ServerNode) handlePrimaryFailure() {
	log.Printf("Nodo %d detectó fallo del primario, iniciando elección", sn.nodeID)

	sn.primaryMutex.Lock()
	sn.isPrimary = false
	sn.primaryID = -1
	sn.primaryMutex.Unlock()

	// Iniciar nueva elección
	go sn.startElection()
}

// Stop detiene el nodo servidor
func (sn *ServerNode) Stop() error {
	log.Printf("Deteniendo nodo %d", sn.nodeID)

	sn.cancel()
	close(sn.stopChan)

	if sn.heartbeatTicker != nil {
		sn.heartbeatTicker.Stop()
	}

	if sn.httpServer != nil {
		return sn.httpServer.Shutdown(context.Background())
	}

	return nil
}

// Handlers HTTP

func (sn *ServerNode) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (sn *ServerNode) handleReplicate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	var event Event
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, "Error decodificando evento", http.StatusBadRequest)
		return
	}

	// Aplicar evento al estado local
	sn.stateMutex.Lock()
	sn.state.SequenceNumber = event.ID
	sn.state.EventLog = append(sn.state.EventLog, event)
	sn.stateMutex.Unlock()

	// Guardar estado
	if err := sn.saveState(); err != nil {
		log.Printf("Error guardando estado: %v", err)
		http.Error(w, "Error interno", http.StatusInternalServerError)
		return
	}

	log.Printf("Nodo %d replicó evento: %+v", sn.nodeID, event)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Evento replicado"))
}

func (sn *ServerNode) handleElection(w http.ResponseWriter, r *http.Request) {
	log.Printf("Nodo %d recibió mensaje de elección", sn.nodeID)

	// Iniciar elección propia
	go sn.startElection()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Elección iniciada"))
}

func (sn *ServerNode) handleCoordinator(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	var data map[string]int
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Error decodificando datos", http.StatusBadRequest)
		return
	}

	primaryID := data["primary_id"]

	sn.primaryMutex.Lock()
	sn.isPrimary = false
	sn.primaryID = primaryID
	sn.primaryMutex.Unlock()

	log.Printf("Nodo %d reconoce al nodo %d como primario", sn.nodeID, primaryID)

	// Iniciar monitoreo del nuevo primario
	go sn.startPrimaryMonitoring()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Coordinador reconocido"))
}

func (sn *ServerNode) handleSync(w http.ResponseWriter, r *http.Request) {
	sn.primaryMutex.RLock()
	isPrimary := sn.isPrimary
	sn.primaryMutex.RUnlock()

	if !isPrimary {
		http.Error(w, "Este nodo no es primario", http.StatusForbidden)
		return
	}

	sn.stateMutex.RLock()
	stateCopy := sn.state
	sn.stateMutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stateCopy)
}

func (sn *ServerNode) handleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}

	sn.primaryMutex.RLock()
	isPrimary := sn.isPrimary
	sn.primaryMutex.RUnlock()

	if !isPrimary {
		// Redirigir al primario
		sn.primaryMutex.RLock()
		primaryID := sn.primaryID
		sn.primaryMutex.RUnlock()

		if primaryID == -1 {
			http.Error(w, "No hay primario disponible", http.StatusServiceUnavailable)
			return
		}

		primaryURL := sn.getNodeURL(primaryID) + "/event"

		// Leer el cuerpo de la petición
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Error leyendo cuerpo", http.StatusBadRequest)
			return
		}

		// Reenviar al primario
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Post(primaryURL, "application/json", bytes.NewBuffer(body))
		if err != nil {
			http.Error(w, "Error reenviando al primario", http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		// Copiar respuesta
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	// Este nodo es primario, procesar evento
	var eventData map[string]string
	if err := json.NewDecoder(r.Body).Decode(&eventData); err != nil {
		http.Error(w, "Error decodificando evento", http.StatusBadRequest)
		return
	}

	// Incrementar número de secuencia
	sn.stateMutex.Lock()
	sn.state.SequenceNumber++
	newEvent := Event{
		ID:    sn.state.SequenceNumber,
		Value: eventData["value"],
	}
	sn.stateMutex.Unlock()

	// Replicar a todos los nodos secundarios
	success := sn.replicateToAll(newEvent)
	if !success {
		http.Error(w, "Error replicando evento", http.StatusInternalServerError)
		return
	}

	// Guardar estado local
	sn.stateMutex.Lock()
	sn.state.EventLog = append(sn.state.EventLog, newEvent)
	sn.stateMutex.Unlock()

	if err := sn.saveState(); err != nil {
		log.Printf("Error guardando estado: %v", err)
		http.Error(w, "Error interno", http.StatusInternalServerError)
		return
	}

	log.Printf("Nodo %d procesó evento: %+v", sn.nodeID, newEvent)

	response := map[string]interface{}{
		"event_id": newEvent.ID,
		"message":  "Evento procesado exitosamente",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (sn *ServerNode) handleStatus(w http.ResponseWriter, r *http.Request) {
	sn.primaryMutex.RLock()
	isPrimary := sn.isPrimary
	primaryID := sn.primaryID
	sn.primaryMutex.RUnlock()

	sn.stateMutex.RLock()
	sequenceNumber := sn.state.SequenceNumber
	sn.stateMutex.RUnlock()

	status := map[string]interface{}{
		"node_id":         sn.nodeID,
		"is_primary":      isPrimary,
		"primary_id":      primaryID,
		"sequence_number": sequenceNumber,
		"host":            sn.host,
		"port":            sn.port,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// replicateToAll replica un evento a todos los nodos secundarios
func (sn *ServerNode) replicateToAll(event Event) bool {
	var wg sync.WaitGroup
	successChan := make(chan bool, len(sn.config.Nodes))

	for _, node := range sn.config.Nodes {
		if node.ID != sn.nodeID {
			wg.Add(1)
			go func(targetNode Node) {
				defer wg.Done()
				success := sn.replicateToNode(targetNode.ID, event)
				successChan <- success
			}(node)
		}
	}

	wg.Wait()
	close(successChan)

	// Verificar que todos los nodos respondieron exitosamente
	for success := range successChan {
		if !success {
			return false
		}
	}

	return true
}

// replicateToNode replica un evento a un nodo específico
func (sn *ServerNode) replicateToNode(targetNodeID int, event Event) bool {
	url := sn.getNodeURL(targetNodeID) + "/replicate"

	jsonData, err := json.Marshal(event)
	if err != nil {
		log.Printf("Error serializando evento: %v", err)
		return false
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Error replicando a nodo %d: %v", targetNodeID, err)
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// syncWithPrimary sincroniza el estado con el primario
func (sn *ServerNode) syncWithPrimary() error {
	sn.primaryMutex.RLock()
	primaryID := sn.primaryID
	sn.primaryMutex.RUnlock()

	if primaryID == -1 {
		return fmt.Errorf("no hay primario disponible")
	}

	url := sn.getNodeURL(primaryID) + "/sync"

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("error contactando primario: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("primario respondió con estado %d", resp.StatusCode)
	}

	var primaryState State
	if err := json.NewDecoder(resp.Body).Decode(&primaryState); err != nil {
		return fmt.Errorf("error decodificando estado del primario: %v", err)
	}

	// Actualizar estado local
	sn.stateMutex.Lock()
	sn.state = primaryState
	sn.stateMutex.Unlock()

	// Guardar estado
	if err := sn.saveState(); err != nil {
		return fmt.Errorf("error guardando estado sincronizado: %v", err)
	}

	log.Printf("Nodo %d sincronizado con primario %d", sn.nodeID, primaryID)
	return nil
}

func main() {
	if len(os.Args) != 3 {
		fmt.Println("Uso: go run main.go <config.json> <node_id>")
		os.Exit(1)
	}

	configPath := os.Args[1]
	nodeID, err := strconv.Atoi(os.Args[2])
	if err != nil {
		fmt.Printf("Error parseando node_id: %v\n", err)
		os.Exit(1)
	}

	// Crear nodo
	node, err := NewServerNode(configPath, nodeID)
	if err != nil {
		log.Fatalf("Error creando nodo: %v", err)
	}

	// Iniciar nodo
	if err := node.Start(); err != nil {
		log.Fatalf("Error iniciando nodo: %v", err)
	}

	// Esperar señal de interrupción
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	// Detener nodo
	if err := node.Stop(); err != nil {
		log.Printf("Error deteniendo nodo: %v", err)
	}
}
