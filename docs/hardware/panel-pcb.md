# Placa a medida del panel del vigilante (HAT para Raspberry Pi 3 B+)

Circuito lógico, lista de conexiones y lista de materiales para sustituir la placa de prototipos por una PCB que solo haya que soldar. **Es un diseño listo para dibujar, no una placa terminada**: no se ha abierto en ningún programa de PCB, así que no hay ficheros Gerber ni se han pasado las comprobaciones de reglas (ERC/DRC). Ver "Qué no está verificado".

## Ficheros

| Fichero | Contenido |
|---|---|
| `panel-pcb-esquema.svg` | Esquema lógico (se abre en cualquier navegador) |
| `panel-pcb-netlist.csv` | Lista de conexiones: qué pines van a cada red |
| `panel-pcb-bom.csv` | Lista de materiales con valores y footprints sugeridos |
| `tests/lab_watchdog/test_panel_pcb.py` | Comprueba que esquema, conexiones, materiales y los pines del software son coherentes entre sí |

## Qué lleva la placa

| Función | Componente | Pin físico (BCM) |
|---|---|---|
| Zumbador activo 5 V | LS1 + transistor Q1 (S8050) con R5 1 kΩ y R6 10 kΩ, C1 100 nF | 11 (GPIO17); 5 V del pin 2 |
| LED verde: monitorización del lab | D1 + R1 330 Ω | 13 (GPIO27) |
| LED rojo: algo falla | D2 + R2 330 Ω | 15 (GPIO22) |
| LED azul: Pi lista y funcionando | D3 + R3 100 Ω | 18 (GPIO24) |
| LED azul testigo de corriente | D4 + R4 100 Ω, directo a 3,3 V | 1 (3V3) |
| Botón de silencio | SW1 | 16 (GPIO23) |
| Botón de encendido y apagado | SW2 | 5 (GPIO3) |
| Bus 1-Wire para 2 sensores DS18B20 | J2 (bornera) + R7 4,7 kΩ | 7 (GPIO4) |
| Reservado para la SIM | J3 (1×6), **sin soldar** hasta elegir módulo | 8 (TXD), 10 (RXD), 22 (GPIO25, PWRKEY), GND y 3,3 V |

Las resistencias de los LED azules son de 100 Ω porque un LED azul necesita unos 3 V y con 330 Ω apenas brilla. Un LED rojo o verde con 100 Ω brillaría demasiado; no los intercambies.

## Por qué cada elemento

- **Q1 y R5:** el zumbador va a 5 V y un GPIO solo da 3,3 V y poca corriente; el transistor lo conmuta por el lado de masa. R5 limita la corriente de base (≈ 2,6 mA, de sobra para saturarlo).
- **R6 (pull-down de 10 kΩ en la base):** la Pi arranca con los GPIO flotando; sin R6 el zumbador podría sonar un instante.
- **C1:** desacopla los 5 V junto al zumbador.
- **GPIO3 para el botón de encendido:** es el único pin que puede despertar una Pi apagada, y ya lleva su pull-up en la Pi. Se usa con el overlay `gpio-shutdown`.
- **Cada LED con su resistencia y el cátodo a masa:** corriente de 1 a 4 mA por LED, muy por debajo de los 16 mA que admite un pin.
- **El LED testigo cuelga de 3,3 V y no de un GPIO:** así sigue encendido con la Pi apagada con el botón, que es justo lo que debe mostrar.

## Especificaciones de la placa (para pedirla)

| Parámetro | Valor propuesto |
|---|---|
| Formato | HAT de Raspberry Pi: 65 × 56 mm, esquinas redondeadas de 3 mm |
| Taladros de fijación | 4 de Ø2,75 mm (M2,5), a 3,5 mm de los bordes, separación de 58 × 49 mm |
| Capas / espesor | 2 capas, FR4 de 1,6 mm, cobre de 1 oz |
| Conector a la Pi | J1: hembra 2×20 de 2,54 mm en la **cara inferior** de la placa |
| Plano de masa | Relleno completo en la cara inferior; **conectar todos los GND** de J1 |
| Pistas | 0,25 mm de señal; 0,5 mm para los 5 V del zumbador y para masa |
| Acabado | HASL sin plomo o ENIG; serigrafía con las referencias y la polaridad |

**Verifica las medidas contra la especificación oficial de HAT de Raspberry Pi antes de pedir.** Las cifras de arriba son las habituales, pero no las he podido comprobar contra el documento.

## Colocación y diseño

- **Orientación en el rack:** coloca los LED y el zumbador en el borde que quede de cara al frente. Los LED de 5 mm por la cara superior, en fila: verde, rojo, azul y testigo.
- **Botones** en el borde, accesibles con la placa montada en el rack. Si quieres evitar un pulsón accidental en SW2 (apaga la Pi), colócalo algo hundido o aparte de SW1.
- **Zumbador** a un lado, lejos de J2 y de los pines de los sensores.
- **Polaridad marcada** en la serigrafía: ánodo/cátodo de cada LED, `+` del zumbador, orden E-B-C de Q1 y numeración de J2 y J3.
- **Cara inferior:** además del J1, deja libre la zona de la cabecera PoE de 4 pines de la Pi 3 B+ (junto al conector GPIO) o un hueco en la placa; comprueba la altura del conector hembra para que no toque componentes de la Pi.
- **Q1:** el S8050 tiene el orden de patas **E-B-C**. Un BC337 es **C-B-E**: no es intercambiable sin cambiar el footprint.

## Cómo hacerla y pedirla

1. **Dibujar el esquema** en KiCad o EasyEDA a partir de `panel-pcb-esquema.svg` y `panel-pcb-netlist.csv`. Los símbolos genéricos bastan (`R`, `C`, `LED`, `Q_NPN_EBC`, `SW_Push`, `Buzzer`, conectores).
2. **Asignar los footprints** de `panel-pcb-bom.csv`. Los nombres son los de las bibliotecas estándar de KiCad, pero compruébalos en tu versión.
3. **Trazar la placa** con las especificaciones de arriba y pasar ERC y DRC.
4. **Exportar los Gerber** y el fichero de taladros, y subirlos a un fabricante (JLCPCB, PCBWay y similares). Para piezas de montaje en agujero (THT) lo normal es pedir solo la placa y soldar a mano. Cinco unidades cuestan unos pocos euros más el envío.
5. **Pedir también** los conectores y el resto de la lista de materiales por separado.

Con KiCad instalado en tu Mac, yo puedo generar el esquema, pasarle ERC y, cuando la placa esté colocada y trazada, DRC y exportar los Gerber por línea de comandos. **La colocación y el trazado de las pistas** los tendrías que hacer tú en KiCad (o con un autorruteador) y yo lo revisaría. Sin KiCad, solo puedo dejarte lo que hay aquí.

## Antes de pedir: medidas que faltan

| Qué | Por qué |
|---|---|
| **Zumbador:** diámetro y separación entre patas | El footprint propuesto es de 12 mm con 7,6 mm de separación; si el tuyo es otro, cambia el footprint |
| **LED:** 3 mm o 5 mm | La lista propone 5 mm |
| **Pulsadores:** si son táctiles de 6 × 6 mm de 4 patas | Es lo que supone el footprint |
| **Sensores de temperatura:** si traen resistencia de pull-up | Si la traen, no soldar R7 |
| **Qué LED va en D4** | Si no es azul ni blanco, R4 debe ser de 330 Ω |

## Al recibir la placa, antes de enchufarla a la Pi

Con un multímetro y **sin la Pi puesta**:

1. Continuidad entre todos los pines GND de J1.
2. Sin continuidad entre 5 V y GND, ni entre 3,3 V y GND, ni entre 5 V y 3,3 V.
3. Orden de patas de Q1 y polaridad de LED y zumbador.

Después, con la Pi encendida y el servicio parado, `lab-watchdog test-indicator` recorre los LED, el zumbador y el botón de silencio. El botón de encendido y apagado se prueba apagando y encendiendo la Pi.

## Ampliaciones previstas y qué falta en el software

- **Sensores de temperatura (DS18B20).** El hardware está previsto en J2 (GPIO4). El software **no lo lee todavía**: haría falta el overlay `w1-gpio` y leer el bus en el vigilante, con su alerta y su prueba.
- **SIM.** J3 reserva el UART (GPIO14 y 15) y GPIO25 para encender el módulo. Cuando se elija, hay que comprobar: la tensión de las señales del módulo (muchos trabajan a 2,8 V o 1,8 V y requieren adaptar niveles), que el módulo se **alimente aparte** (los picos de transmisión llegan a 2 A y la Pi ya sufrió bajo voltaje) y la configuración del UART en la Pi 3, donde el Bluetooth usa el UART principal y hay que reasignarlo.

## Qué no está verificado

- La placa no se ha abierto en ningún programa de PCB: **no hay ERC, DRC ni Gerber**, y los nombres de footprint son los habituales pero sin comprobar.
- Las medidas del HAT y la zona libre de la cabecera PoE de la Pi 3 B+.
- El orden de patas del S8050 y las medidas de tu zumbador, LED y pulsadores.
- Que el esquema equivale al montaje actual en la placa de prototipos: se ha comprobado contra la configuración del software y contra lo descrito en la sesión, no sobre la placa física.
