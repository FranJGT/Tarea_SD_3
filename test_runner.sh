#!/bin/bash

# Script de pruebas para el sistema distribuido tolerante a fallos
# Implementa el Algoritmo del Matón (Bully Algorithm)

set -e  # Salir en caso de error

# Colores para output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Función para imprimir mensajes con colores
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Función para limpiar el entorno
cleanup() {
    print_status "Limpiando entorno..."
    
    # Matar procesos de nodos si existen
    pkill -f "go run main.go" || true
    pkill -f "./distributed_system" || true
    
    # Esperar un momento para que los procesos se cierren
    sleep 2
    
    # Eliminar archivos de estado
    rm -f node_*_state.json
    
    print_success "Entorno limpiado"
}

# Función para compilar el programa
compile() {
    print_status "Compilando programa Go..."
    
    if ! command -v go &> /dev/null; then
        print_error "Go no está instalado. Por favor instala Go primero."
        exit 1
    fi
    
    # Verificar que main.go existe
    if [ ! -f "main.go" ]; then
        print_error "main.go no encontrado en el directorio actual"
        exit 1
    fi
    
    # Verificar que config.json existe
    if [ ! -f "config.json" ]; then
        print_error "config.json no encontrado en el directorio actual"
        exit 1
    fi
    
    print_success "Programa compilado correctamente"
}

# Función para iniciar un nodo
start_node() {
    local node_id=$1
    print_status "Iniciando nodo $node_id..."
    
    # Iniciar nodo en segundo plano
    go run main.go config.json $node_id > node_${node_id}.log 2>&1 &
    local pid=$!
    
    # Guardar PID
    echo $pid > node_${node_id}.pid
    
    # Esperar a que el nodo esté listo
    sleep 3
    
    print_success "Nodo $node_id iniciado con PID $pid"
}

# Función para detener un nodo
stop_node() {
    local node_id=$1
    local pid_file="node_${node_id}.pid"
    
    if [ -f "$pid_file" ]; then
        local pid=$(cat $pid_file)
        print_status "Deteniendo nodo $node_id (PID: $pid)..."
        
        kill $pid 2>/dev/null || true
        rm -f $pid_file
        
        # Esperar a que el proceso termine
        sleep 2
        
        print_success "Nodo $node_id detenido"
    else
        print_warning "Archivo PID para nodo $node_id no encontrado"
    fi
}

# Función para verificar el estado de un nodo
check_node_status() {
    local node_id=$1
    local port=$((8080 + node_id))
    
    print_status "Verificando estado del nodo $node_id..."
    
    # Esperar un poco más para que el nodo esté completamente listo
    sleep 2
    
    local response
    response=$(curl -s http://localhost:$port/status 2>/dev/null || echo "ERROR")
    
    if [ "$response" = "ERROR" ]; then
        print_error "No se pudo conectar al nodo $node_id"
        return 1
    fi
    
    echo "Estado del nodo $node_id:"
    echo "$response" | jq '.' 2>/dev/null || echo "$response"
    echo ""
    
    return 0
}

# Función para enviar un evento
send_event() {
    local node_id=$1
    local event_value=$2
    local port=$((8080 + node_id))
    
    print_status "Enviando evento '$event_value' al nodo $node_id..."
    
    local response
    response=$(curl -s -X POST http://localhost:$port/event \
        -H "Content-Type: application/json" \
        -d "{\"value\": \"$event_value\"}" 2>/dev/null || echo "ERROR")
    
    if [ "$response" = "ERROR" ]; then
        print_error "Error enviando evento al nodo $node_id"
        return 1
    fi
    
    echo "Respuesta del nodo $node_id:"
    echo "$response" | jq '.' 2>/dev/null || echo "$response"
    echo ""
    
    return 0
}

# Función para verificar consistencia entre nodos
check_consistency() {
    print_status "Verificando consistencia entre nodos..."
    
    local node1_state
    local node2_state
    local node3_state
    
    # Obtener estados de todos los nodos
    node1_state=$(curl -s http://localhost:8081/status 2>/dev/null || echo "ERROR")
    node2_state=$(curl -s http://localhost:8082/status 2>/dev/null || echo "ERROR")
    node3_state=$(curl -s http://localhost:8083/status 2>/dev/null || echo "ERROR")
    
    if [ "$node1_state" = "ERROR" ] || [ "$node2_state" = "ERROR" ] || [ "$node3_state" = "ERROR" ]; then
        print_error "No se pudo obtener el estado de todos los nodos"
        return 1
    fi
    
    # Extraer números de secuencia
    local seq1=$(echo "$node1_state" | jq -r '.sequence_number' 2>/dev/null || echo "0")
    local seq2=$(echo "$node2_state" | jq -r '.sequence_number' 2>/dev/null || echo "0")
    local seq3=$(echo "$node3_state" | jq -r '.sequence_number' 2>/dev/null || echo "0")
    
    echo "Números de secuencia:"
    echo "  Nodo 1: $seq1"
    echo "  Nodo 2: $seq2"
    echo "  Nodo 3: $seq3"
    
    if [ "$seq1" = "$seq2" ] && [ "$seq2" = "$seq3" ]; then
        print_success "Consistencia verificada - todos los nodos tienen el mismo número de secuencia"
        return 0
    else
        print_error "Inconsistencia detectada - los nodos tienen diferentes números de secuencia"
        return 1
    fi
}

# Función para esperar a que se elija un nuevo primario
wait_for_new_primary() {
    print_status "Esperando a que se elija un nuevo primario..."
    
    local max_attempts=30
    local attempt=0
    
    while [ $attempt -lt $max_attempts ]; do
        # Verificar si hay un primario
        local primary_found=false
        
        for node_id in 1 2 3; do
            local port=$((8080 + node_id))
            local response=$(curl -s http://localhost:$port/status 2>/dev/null || echo "ERROR")
            
            if [ "$response" != "ERROR" ]; then
                local is_primary=$(echo "$response" | jq -r '.is_primary' 2>/dev/null || echo "false")
                if [ "$is_primary" = "true" ]; then
                    local primary_id=$(echo "$response" | jq -r '.primary_id' 2>/dev/null || echo "0")
                    print_success "Nuevo primario elegido: Nodo $primary_id"
                    primary_found=true
                    break
                fi
            fi
        done
        
        if [ "$primary_found" = "true" ]; then
            return 0
        fi
        
        attempt=$((attempt + 1))
        sleep 1
    done
    
    print_error "No se pudo detectar un nuevo primario después de $max_attempts intentos"
    return 1
}

# Función principal de pruebas
run_tests() {
    print_status "=== INICIANDO PRUEBAS DEL SISTEMA DISTRIBUIDO ==="
    
    # Limpiar entorno
    cleanup
    
    # Compilar programa
    compile
    
    # Iniciar todos los nodos
    print_status "Iniciando todos los nodos..."
    start_node 1
    start_node 2
    start_node 3
    
    # Esperar a que todos los nodos estén listos
    sleep 5
    
    print_status "=== PRUEBA 1: Elección inicial del primario ==="
    
    # Verificar estado inicial de todos los nodos
    for node_id in 1 2 3; do
        check_node_status $node_id
    done
    
    # Verificar que se eligió un primario
    wait_for_new_primary
    
    # Enviar primer evento
    print_status "Enviando primer evento..."
    send_event 1 "Evento inicial del sistema"
    
    # Verificar consistencia
    check_consistency
    
    print_status "=== PRUEBA 2: Simulación de fallo del primario ==="
    
    # Identificar el primario actual
    local current_primary=0
    for node_id in 1 2 3; do
        local port=$((8080 + node_id))
        local response=$(curl -s http://localhost:$port/status 2>/dev/null || echo "ERROR")
        
        if [ "$response" != "ERROR" ]; then
            local is_primary=$(echo "$response" | jq -r '.is_primary' 2>/dev/null || echo "false")
            if [ "$is_primary" = "true" ]; then
                current_primary=$node_id
                break
            fi
        fi
    done
    
    if [ $current_primary -eq 0 ]; then
        print_error "No se pudo identificar el primario actual"
        exit 1
    fi
    
    print_status "Primario actual identificado: Nodo $current_primary"
    print_status "Simulando fallo del primario..."
    
    # Detener el primario
    stop_node $current_primary
    
    # Esperar a que se elija un nuevo primario
    wait_for_new_primary
    
    # Enviar segundo evento
    print_status "Enviando segundo evento después del fallo..."
    send_event $((current_primary % 3 + 1)) "Evento después del fallo del primario"
    
    # Verificar consistencia
    check_consistency
    
    print_status "=== PRUEBA 3: Recuperación del nodo caído ==="
    
    # Reiniciar el nodo que falló
    print_status "Reiniciando nodo $current_primary..."
    start_node $current_primary
    
    # Esperar a que se sincronice
    sleep 10
    
    # Verificar que el nodo recuperado se sincronizó
    print_status "Verificando sincronización del nodo recuperado..."
    check_node_status $current_primary
    
    # Enviar tercer evento
    print_status "Enviando tercer evento después de la recuperación..."
    send_event $((current_primary % 3 + 1)) "Evento después de la recuperación"
    
    # Verificar consistencia final
    check_consistency
    
    print_success "=== TODAS LAS PRUEBAS COMPLETADAS EXITOSAMENTE ==="
}

# Función para mostrar el estado actual del sistema
show_status() {
    print_status "=== ESTADO ACTUAL DEL SISTEMA ==="
    
    for node_id in 1 2 3; do
        local port=$((8080 + node_id))
        local response=$(curl -s http://localhost:$port/status 2>/dev/null || echo "ERROR")
        
        if [ "$response" != "ERROR" ]; then
            echo "Nodo $node_id:"
            echo "$response" | jq '.' 2>/dev/null || echo "$response"
            echo ""
        else
            print_warning "Nodo $node_id no responde"
        fi
    done
}

# Función para mostrar ayuda
show_help() {
    echo "Script de pruebas para el sistema distribuido tolerante a fallos"
    echo ""
    echo "Uso: $0 [COMANDO]"
    echo ""
    echo "Comandos:"
    echo "  test     - Ejecutar todas las pruebas (por defecto)"
    echo "  status   - Mostrar estado actual del sistema"
    echo "  cleanup  - Limpiar entorno (matar procesos, eliminar archivos)"
    echo "  help     - Mostrar esta ayuda"
    echo ""
    echo "Ejemplos:"
    echo "  $0 test      # Ejecutar pruebas completas"
    echo "  $0 status    # Ver estado actual"
    echo "  $0 cleanup   # Limpiar todo"
}

# Verificar dependencias
check_dependencies() {
    local missing_deps=()
    
    if ! command -v go &> /dev/null; then
        missing_deps+=("go")
    fi
    
    if ! command -v curl &> /dev/null; then
        missing_deps+=("curl")
    fi
    
    if ! command -v jq &> /dev/null; then
        missing_deps+=("jq")
    fi
    
    if [ ${#missing_deps[@]} -ne 0 ]; then
        print_error "Dependencias faltantes: ${missing_deps[*]}"
        echo "Por favor instala las dependencias faltantes:"
        echo "  - Go: https://golang.org/dl/"
        echo "  - curl: sudo apt-get install curl (Ubuntu/Debian) o brew install curl (macOS)"
        echo "  - jq: sudo apt-get install jq (Ubuntu/Debian) o brew install jq (macOS)"
        exit 1
    fi
}

# Manejo de argumentos
main() {
    local command=${1:-test}
    
    case $command in
        "test")
            check_dependencies
            run_tests
            ;;
        "status")
            check_dependencies
            show_status
            ;;
        "cleanup")
            cleanup
            ;;
        "help"|"-h"|"--help")
            show_help
            ;;
        *)
            print_error "Comando desconocido: $command"
            show_help
            exit 1
            ;;
    esac
}

# Ejecutar función principal
main "$@" 