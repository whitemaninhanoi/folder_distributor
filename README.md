# Folder Distributor - Manual Rápido

Este script está diseñado para monitorear una carpeta y distribuir su contenido a múltiples destinos, usando el método más rápido posible según el sistema operativo (Robocopy en Windows, Rsync en Unix).

## ⚡ Requisitos

### Windows:

* Nada que instalar. El script usa **Robocopy**, que ya viene con Windows.

### macOS / Linux:

* Tener instalado `rsync` (ya viene en la mayoría de los sistemas).

---

## 🌐 Uso básico

```bash
./folder-distributor --watch "/ruta/a/monitorear" --use-tooling --parallel 2
```

### Argumentos disponibles:

| Flag               | Descripción                                                               |
| ------------------ |---------------------------------------------------------------------------|
| `--watch`          | Carpeta a monitorear. Obligatorio.                                        |
| `--use-tooling`    | Usa `rsync` (Linux/macOS) o `robocopy` (Windows).                         |
| `--parallel`       | Copias simultáneas (entre 1 y 4).                                         |
| `--timestamp-name` | Renombra carpeta con timestamp (`YYYY-MM-DD HH:mm NOMBRE_DE_LA_CARPETA`). |
| `--save-prompt`    | Guarda logs detallados de cada operación en la carpeta `logs`.            |

---

## 📂 Comportamiento

* Solo distribuye carpetas que **tienen un recibo de entrega** (archivo `.txt` con mismo nombre).
* Una vez copiada una carpeta, no se vuelve a distribuir.
* Se genera un log por carpeta distribuida con progreso y velocidad.
* Se guarda un archivo `metrics.csv` con tiempos, tamaños y método usado por destino.

---

## 📊 Ejemplo de salida

```
[COPY] [/DEST01] [##############------------] Progreso: 46.3% | Copiado: 824.4 MB / 1780.6 MB | ETA: 1m20s
[OK] Copia completa a: /DEST01
[TIME] Distribución finalizada en: 2m14s
```

---

## 🔧 Consejos de rendimiento

* Usar `--use-tooling` para activar Robocopy/Rsync (más rápido que copiar archivo por archivo).
* Subir `--parallel` a 2–4 si hay suficientes recursos (RAM, CPU).
* Ejecutar el binario desde disco local, no desde red.

---

## 🔎 Logs generados

* `output.log`: muestra la actividad de copiado.
* `metrics.csv`: tiempo y tamaño de cada copia.
* `setup.log`: errores generales del sistema.

---

## 🌟 Ejecución automática (opcional)

Se puede configurar el script para que se ejecute al iniciar Windows con el programador de tareas (`taskschd.msc`) o crear un acceso directo en la carpeta `shell:startup`.
