# 20_APROVISIONAMIENTO — aprovisionamiento del nodo

Esta carpeta son 22 guiones de shell que configuran el nodo, uno por tema, y un
orquestador que los llama en orden.

**Cada guion ES la configuración.** No hay plantillas de Ansible ni imágenes de
Docker: lo que el nodo tiene puesto es lo que estos archivos escriben. Si un
cambio no está aquí, no existe y el nodo no se puede reconstruir.

---

## Instalación

```sh
# 1. los seis valores de su red, una sola vez
sudo install -d -m 0755 /etc/nas
sudo cp ajustes.conf.ejemplo /etc/nas/ajustes.conf
sudo nano /etc/nas/ajustes.conf

# 2. todo lo demás
sudo ./instalar.sh
```

Al terminar, el NAS responde en `http://SU_IP` y comparte `\\SU_IP\datos` por SMB.

Para el acceso desde fuera de la red local —opcional, y exige una cuenta gratuita
de DuckDNS— hay una segunda pasada:

```sh
sudo ./instalar.sh --remoto
```

---

## El archivo de configuración

`ajustes.conf.ejemplo` lleva seis valores y una explicación al lado de cada uno.
Son los únicos que cambian entre instalaciones:

| | Qué es | Dónde mirarlo |
|---|---|---|
| `RED` | Su red doméstica, en CIDR | `ip -4 -o addr show` |
| `NODO_IP` | La dirección fija de este nodo | la que le reserve en el router |
| `INTERFAZ` | El cable de red | `ip -o link show` |
| `DISCO` | El disco de datos. **Se formatea entero** | `lsblk -o NAME,SIZE,MODEL,SERIAL,TRAN` |
| `DISCO_MODELO` | Qué modelo se espera en `DISCO` | `lsblk -ndo MODEL /dev/sdX` |
| `RED_TUNEL` | La red privada del túnel | déjelo como está si no usa el túnel |

**Se editan a mano y nada se detecta solo.** Detectar la red de la interfaz es
fácil y acierta casi siempre; el «casi» es el problema. Un nodo con dos
interfaces, una Wi-Fi todavía conectada o un DHCP que aún no ha dado la
dirección definitiva producen una detección *plausible y equivocada*, y ese es
el peor modo de fallo posible aquí: el nodo arranca, no falla nada, y clasifica
todo el tráfico local como si viniera de Internet.

**El nombre DDNS no está en este archivo.** Vive en `/etc/nas/ddns.conf`, junto
al testigo de la cuenta, porque es una credencial y porque es opcional. Lo crea
usted a mano la primera vez que ejecute `11_ddns.sh`, que le dice cómo.

---

## El orquestador

Tres cosas, y ninguna de ellas es instalar:

1. **Pone los guiones en orden.** El orden real no es el de los números —`07` va
   antes que `06`— y hasta ahora solo se podía deducir leyendo las cabeceras.
2. **Comprueba antes de tocar nada.** Todo lo que, de fallar a mitad, dejaría el
   disco ya formateado y el servicio a medias: que el disco es el que usted
   cree, que la interfaz existe, que la dirección cuadra, que hay red para
   `apt`. Los fallos salen **todos a la vez**, no de uno en uno.
3. **Resuelve el binario.** Ver más abajo.

Tiene dos banderas y no más:

| Bandera | Para qué |
|---|---|
| `--remoto` | La segunda lista: DDNS, túnel, NAT y TLS |
| `--desde NN` | Retomar en ese paso tras arreglar un fallo, sin repetir los anteriores |

`--desde` existe porque `01_preparar_disco.sh` **formatea**. Volver a lanzarlo
todo tras un fallo en el paso 8 destruiría los datos, así que no hay ningún
«reintentar desde el principio».

---

## El binario de `nasd`

El servicio web es un binario de Go sin dependencias. `instalar.sh` lo resuelve
así, en este orden:

1. **Si ya hay uno en `/tmp/nasd`**, lo usa.
2. **Si este nodo tiene Go instalado**, lo compila aquí mismo.
3. **Si no**, se para sin tocar nada y escribe el comando exacto para
   compilarlo en su PC y enviarlo por `scp`.

Una Raspberry Pi 3B+ con 512 MB cae en el caso 3 y eso es lo normal: compilar
Go ahí compite con el propio servicio. Una Pi más grande, o un PC con Linux,
cae en el 2.

Los dos programas auxiliares escritos en C —`nas-miniatura` y `nas-sensor`— se
quedan fuera a propósito. Sin ellos el NAS funciona igual: las miniaturas
degradan a un 404 y el sensor de paquetes es un servicio aparte que instala
`19_sensor.sh`. Compilarlos exigiría una cadena de C en el nodo, que es
justamente lo que el primer paso acaba de retirar.

---

## Orden de ejecución

```
  00  endurecer el nodo         quita escritorio y servicios de más
  01  preparar el disco         FORMATEA. ext4, montaje por UUID, usuario nas
  02  instalar Samba            el recurso \\nodo\datos
  03  cortafuegos               política DENY. PREGUNTA antes de aplicar
  04  verificar                 mide; no instala nada
  05  instalar el servicio      el binario, la unidad systemd y el TOML
  07  la credencial             una sola contraseña, para la web y para SMB
  06  verificar la web          necesita la credencial de 07 para entrar
  08  observabilidad            diario persistente y acotado, watchdog
  10  resiliencia del disco     remontaje tras una caída del bus USB
  17  respaldar la identidad    claves y credenciales, lo irreemplazable
  09  verificar la operación    el veredicto final
```

Tres cosas que sorprenden y que tienen respuesta:

- **`07` antes que `06`.** `06_verificar_web.sh` prueba la web *autenticada*.
  Sin la contraseña que fija `07` no hay con qué entrar, así que probaría un
  candado sin llave.
- **`03` pregunta y no se le puede quitar la pregunta.** Aplica una política
  DENY por defecto; si usted está conectado por SSH desde fuera de `RED`,
  pierde el acceso en ese instante. Hay un modo no interactivo (`--si`) pero
  solo está autorizado para *volver a aplicar* reglas que ya están en vigor.
- **`03` y `05` se ejecutan dos veces** si instala el acceso remoto. No es un
  error: antes del certificado, `05` escribe una configuración sin TLS y `03`
  no abre el 443; después de emitirlo hay que volver a pasar para que lo
  recojan. Los dos son idempotentes, así que repetirlos cuesta segundos.

**No hay ningún `13_`.** Ese número nunca se usó; no falta nada. Y los números
de los archivos no son los mismos que los «pasos» de las cabeceras: `14_` dice
ser el «paso 8» de su fase.

---

## Pasos opcionales

`instalar.sh` no ejecuta nada de esto. Se pide a mano, cuando haga falta:

| Guion | Qué añade | Qué exige antes |
|---|---|---|
| `--remoto` (11, 12, 14, 15) | Nombre estable, túnel WireGuard, NAT y certificado TLS | una cuenta gratuita de DuckDNS |
| `16_ipv6_estable.sh` | Dirección IPv6 fija | termina pidiendo un campo en la pantalla de su router |
| `18_geoip.sh` | País y operador en el panel de seguridad | nada. Es solo presentación |
| `19_sensor.sh` | Sensor de paquetes | su binario se compila en el PC |
| `20` y `21` | Avisos a Telegram y testigo externo | un bot y un chat |
| `22_privilegio_minimo_respaldo.sh` | Recurso SMB aparte para el cliente de respaldo | crear el usuario `respaldo` a mano |

**Un fallo de `09` es esperado en un nodo que solo sirve la LAN**: el respaldo de
identidad no contiene la clave de WireGuard, porque el túnel no existe. No es un
defecto. Se salda entero al ejecutar `--remoto`.

Los verificadores (`04`, `06`, `09`) se saltan solos los bloques que no aplican
y lo dicen. Un fallo por algo que aún no existe enseña a ignorar los fallos.

---

## Cuando un paso falla

Cada guion se detiene al primer problema y dice qué pasó. `instalar.sh` añade
dónde retomar:

```
ERROR: ./08_observabilidad.sh termino con codigo 1.
       Los 7 pasos anteriores SI se aplicaron: el nodo esta a medias.
       Lea el error de arriba, arreglelo, y retome donde se quedo:
         sudo ./instalar.sh --desde 08
```

Cosas que pasan de verdad:

- **«no tiene permiso de ejecucion»** — copió el repositorio desde Windows o
  desde un ZIP. `chmod +x *.sh`.
- **«DISCO no es un dispositivo de bloques»** — los nombres `/dev/sdX` los
  reparte el kernel por orden de aparición y cambian entre arranques. Por eso
  existe `DISCO_MODELO`: es la segunda llave que impide formatear el disco
  equivocado.
- **«no existe la interfaz de red»** — en Raspberry Pi OS Bookworm suele
  llamarse `end0` y no `eth0`.
- **Canceló usted en `01` o en `03`** — no hay nada que arreglar: el nodo se
  quedó justo antes de ese paso.

---

## Fuera del alcance

- **No crea cuentas de usuario de la web.** La primera la crea `07`.
- **No configura su router.** El acceso desde fuera exige abrir un puerto y
  `16_ipv6_estable.sh` termina diciéndole exactamente cuál.
- **No respalda sus datos.** Eso es el cliente de `30_CLIENTE_RESPALDO/`.
- **No decide por usted en lo que destruye.** `01` formatea y `03` puede
  dejarle sin SSH: los dos piden confirmación escrita, y esa pregunta no se
  puede desactivar desde `instalar.sh`.
