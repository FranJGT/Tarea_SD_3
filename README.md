# Sistema Distribuido Tolerante a Fallos

## Descripción

Este proyecto implementa un sistema distribuido tolerante a fallos utilizando el **Algoritmo del Matón (Bully Algorithm)** para la elección de líderes. El sistema está desarrollado completamente en **Go (Golang)** y utiliza comunicación HTTP REST entre nodos.

## Características Principales

- **Algoritmo del Matón**: Implementación completa para elección de líderes
- **Tolerancia a Fallos**: Detección automática de fallos y recuperación
- **Replicación Síncrona**: Estado consistente entre todos los nodos activos
- **Persistencia**: Estado guardado en archivos JSON
- **Comunicación HTTP**: API REST simple y fácil de depurar
- **Monitoreo de Heartbeat**: Detección de fallos mediante heartbeats periódicos

## Arquitectura del Sistema

### Componentes

1. **Módulo de Coordinación**: Implementa el Algoritmo del Matón
2. **Módulo de Monitoreo**: Vigila la salud del primario mediante heartbeats
3. **Módulo de Sincronización**: Maneja la reintegración de nodos recuperados
4. **Módulo de Persistencia**: Guarda y carga el estado del sistema

### Estructura de Archivos

```
T3_SD/
├── main.go              # Código principal del nodo servidor
├── config.json          # Configuración de nodos
├── test_runner.sh       # Script de pruebas automatizadas
├── README.md           # Documentación del proyecto
└── nodo_X.json         # Archivos de estado (generados automáticamente)
```

### Formato de Archivos de Estado

Cada nodo persiste su estado en un archivo con el formato `nodo_X.json`, donde `X` es el ID del nodo. Por ejemplo:
- `nodo_1.json` - Estado del nodo 1
- `nodo_2.json` - Estado del nodo 2  
- `nodo_3.json` - Estado del nodo 3

## Instalación y Configuración

### Prerrequisitos

- **Go 1.19+**: [Descargar Go](https://golang.org/dl/)
- **curl**: Para las pruebas HTTP
- **jq**: Para procesamiento de JSON en las pruebas

### Instalación de Dependencias

#### macOS
```bash
brew install curl jq
```

#### Ubuntu/Debian
```bash
sudo apt-get update
sudo apt-get install curl jq
```

### Configuración

El archivo `config.json` define la configuración de los nodos:

```json
{
  "nodes": [
    {
      "id": 1,
      "host": "localhost",
      "port": 8081
    },
    {
      "id": 2,
      "host": "localhost",
      "port": 8082
    },
    {
      "id": 3,
      "host": "localhost",
      "port": 8083
    }
  ]
}
```

## Uso

### Ejecutar un Nodo Individual

```bash
go run main.go config.json <node_id>
```

Ejemplo:
```bash
go run main.go config.json 1
```

### Ejecutar Pruebas Automatizadas

```bash
./test_runner.sh test
```

### Ver Estado del Sistema

```bash
./test_runner.sh status
```

### Limpiar Entorno

```bash
./test_runner.sh cleanup
```

## API REST

Cada nodo expone los siguientes endpoints:

### Endpoints Públicos

- `GET /status` - Estado actual del nodo
- `POST /event` - Crear nuevo evento (redirige al primario si es secundario)

### Endpoints Internos

- `GET /heartbeat` - Verificación de salud del nodo
- `POST /replicate` - Replicación de eventos desde el primario
- `GET /election` - Iniciar proceso de elección
- `POST /coordinator` - Anunciar nuevo coordinador
- `GET /sync` - Sincronización de estado (solo primario)

## Algoritmo del Matón

### Proceso de Elección

1. **Detección de Fallo**: Un nodo detecta que el primario no responde
2. **Inicio de Elección**: El nodo inicia una elección enviando mensajes a nodos con ID superior
3. **Respuesta de Nodos Superiores**: Si un nodo superior responde, inicia su propia elección
4. **Victoria**: El nodo con ID más alto que no recibe respuesta se convierte en primario
5. **Anuncio**: El nuevo primario anuncia su victoria a todos los demás nodos

### Ventajas del Algoritmo

- **Simplicidad**: Fácil de implementar y entender
- **Eficiencia**: Elección rápida en la mayoría de casos
- **Determinismo**: El nodo con ID más alto siempre gana

## Modelo de Fallos

El sistema utiliza el modelo **Fail-Stop**:
- Los nodos simplemente se detienen cuando fallan
- No hay comportamiento malicioso
- Los fallos son detectables por otros nodos

## Replicación de Estado

### Estructura del Estado

```json
{
  "sequence_number": 8,
  "event_log": [
    {"id": 1, "value": "Evento A"},
    {"id": 2, "value": "Evento B"}
  ]
}
```

### Proceso de Replicación

1. **Recepción de Evento**: El primario recibe un nuevo evento
2. **Incremento de Secuencia**: Incrementa el número de secuencia
3. **Replicación Síncrona**: Envía el evento a todos los secundarios
4. **Confirmación**: Espera confirmación de todos los secundarios
5. **Persistencia**: Guarda el estado en el archivo local

## Pruebas

El script `test_runner.sh` ejecuta tres pruebas principales:

### Prueba 1: Elección Inicial
- Inicia todos los nodos
- Verifica la elección del primario
- Envía un evento inicial
- Verifica consistencia

### Prueba 2: Fallo del Primario
- Identifica el primario actual
- Simula un fallo (mata el proceso)
- Espera elección de nuevo primario
- Envía evento y verifica consistencia

### Prueba 3: Recuperación
- Reinicia el nodo que falló
- Verifica sincronización automática
- Envía evento final
- Verifica consistencia global

## Monitoreo y Logs

### Logs del Sistema

Cada nodo genera logs detallados:
- Inicio y configuración
- Procesos de elección
- Replicación de eventos
- Detección de fallos
- Sincronización

### Verificación de Estado

```bash
# Ver estado de un nodo específico
curl http://localhost:8081/status | jq

# Ver estado de todos los nodos
./test_runner.sh status
```

## Consideraciones de Diseño

### Concurrencia

- Uso de `sync.RWMutex` para proteger acceso al estado
- Goroutines para tareas concurrentes (servidor HTTP, monitoreo)
- Canales para comunicación entre goroutines

### Robustez

- Timeouts en todas las operaciones HTTP
- Manejo de errores en todas las operaciones de red
- Recuperación automática de fallos

### Escalabilidad

- Configuración flexible mediante archivo JSON
- Fácil agregar/remover nodos
- Comunicación HTTP estándar

## Troubleshooting

### Problemas Comunes

1. **Puertos en uso**: Verificar que los puertos 8081-8083 estén libres
2. **Dependencias faltantes**: Instalar Go, curl y jq
3. **Permisos**: Hacer ejecutable el script de pruebas

### Comandos de Diagnóstico

```bash
# Verificar procesos activos
ps aux | grep "go run main.go"

# Verificar puertos en uso
lsof -i :8081
lsof -i :8082
lsof -i :8083

# Ver logs de un nodo
tail -f node_1.log
```

## Contribución

Para contribuir al proyecto:

1. Fork el repositorio
2. Crea una rama para tu feature
3. Implementa los cambios
4. Ejecuta las pruebas
5. Envía un pull request

## Licencia

Este proyecto es para fines educativos y de investigación en sistemas distribuidos.

## Equipo de Desarrollo

### Integrantes del Proyecto

**Nombre del Estudiante**  
*Rol: Desarrollador Principal*  
- Implementación del Algoritmo del Matón
- Desarrollo del sistema de replicación
- Configuración de la API REST
- Documentación técnica

*Nota: Por favor, actualiza esta sección con los nombres reales y roles específicos de los integrantes de tu equipo.*

## Autor

Desarrollado como proyecto universitario de Sistemas Distribuidos. 