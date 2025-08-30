# Design

This document is a work in progress and describes the design of SHEM and the reasoning behind it. You are welcome to provide feedback and improvements.

## Functionality
SHEM's purpose is to control all your electrical devices whose power consumption or generation can be controlled (such as battery storage units or electric vehicle (EV) chargers), typically to minimize energy costs.

This breaks down into these sub-functionalities:

- Obtain future energy price components, e.g., day-ahead auction results published by ENTSO-E (European Network of Transmission System Operators for Electricity).
- Calculate energy prices for feed-in and consumption according to user-supplied functions. These functions can utilize future price components, time-dependent values (e.g., yearly changing tariffs or grid tariffs depending on the time of day), and constants.
- Communicate with electrical devices to send commands (e.g., discharge battery storage at a certain power level, or allow EV to charge with a given maximum power) and read data (e.g., meter data from a smart meter, state of charge of a battery storage unit, or connection state of an EV).
- Forecast time-dependent variables such as solar generation, household and EV power consumption, and EV availability (i.e., when the EV will be connected to the charger).
- Integrate models for devices, e.g., how a battery storage unit works, containing user-definable variables for efficiency, power limitations, and capacity. 
- Allow the user to specify an economic model of how measured values, forecasts, price data and device models are combined in an objective function, i.e., the value that SHEM should optimize for (e.g., energy costs, possibly augmented by a component that accounts for user inconveniences).
- Create an optimized plan for using available flexibility that optimizes the objective function (or its expected value) and update it regularly when new data becomes available.
- Execute the plan by controlling the electric devices.
- Calculate the energy costs (more generally, the objective function) for the actual values that resulted and compare them to both what the device models would have predicted and what these models provide for fallback behavior without optimization.
- Allow the user to test the effects of different optimization methods and the accuracy of the device models by a virtual re-run of historical data.
- Provide interfaces for current and historical data for other software to use for, e.g., graphical display.

An important part of the functionality is to find an optimized schedule for the controllable devices and to execute it. This is detailed in [optimization.md](./optimization.md).

## Security and Safety
This software is meant to control electrical devices whose power lies in the kilowatts range. **It is assumed that all devices controlled by SHEM are safe regardless of the control commands they receive.** The consequences of a security or safety failure with regards to controllable devices are therefore limited to what can be caused by these devices within their defined limits. For example, the battery storage system protects its battery from over- and undercharging. It might therefore charge at the wrong time and incur costs, but it may not catch fire because of undervoltage.

This leaves the following possible consequences of a security or safety failure of a single or a few SHEM instances:
- Financial costs. These might be higher energy costs than would have occurred otherwise, smaller revenues, or increased wear of controllable devices that cost money to replace (e.g., battery cycle count increasing faster than it would have). In typical household installations, these costs range up to, at most, hundreds of Euros or USD per month.
- Inconvenience to the user. For example, an electric vehicle might not be charged when needed, or room temperature might be too high or too low.
- Loss of private data. Energy data might tell a lot about what the occupants of the household do when, for example when they leave and come back for work or for vacation.

However, if a large number of energy management systems are taken over by an attacker (e.g., if a remote exploit is discovered), the possible consequences become much worse. Controlling several gigawatts of power would allow an attacker to overpower even large electrical grids, causing widespread blackouts. This means energy management systems should be especially protected against these kinds of attacks, while at the same time the motivation and resources of attackers will be greatest. Taking down the energy system of an enemy nation is an important military objective, so attacks by nation states on any energy management system that has gained widespread use are to be expected.

SHEM is meant to be used in a decentralized manner, with one instance controlling the electrical devices of only one household. Therefore many instances would have to be successfully attacked, either directly or indirectly (e.g., by manipulating data they all rely on). This mitigates against certain attacks that are possible in more centralized systems, such as an insider manipulating a central server that can control many households. Still, many scenarios for a successful attack targeting many SHEM instances remain. For example, a remote exploit might be found, or an attacker might manipulate the source or object code of SHEM or one of its modules, or a data source that many SHEM instances rely on (e.g., price data) might be manipulated.

An important aim of this project is to take security considerations like these into account during design and development. In the long run, this will hopefully allow us to build a home energy management system that is reasonably secure.

### Protection Goals and Their Priorities
1. Protect the energy system against coordinated attacks that make use of the power controlled by many SHEM instances. As the potential damage protected against by this goal is much larger than that from the others, it takes absolute priority.
2. Protect data from unauthorized access.
3. Protect limits set by the user, e.g., EV battery state at certain times or allowed ranges for room temperatures.
4. Protect against financial losses. 

### Threat and Fault Model
- **Threat Actors:** Should SHEM gain such widespread adoption that the total power of controlled devices can threaten a national or international power grid, this provides motivation for attacking it even for the most sophisticated attackers. Before that happens, it might be of interest for ordinary criminals to gain access to local networks, or for building a bot network. In many cases, an attacker will attempt to gain control over a large number of SHEM instances.

- **Attack Vectors**
  - exploiting security vulnerabilities in SHEM or in its dependencies
  - supply-chain attacks through software dependencies
  - backdoor introduced into SHEM (by an innocent-looking pull request or by taking over a developer account)
  - manipulating data on publicly accessible platforms such as energy prices
  - provoking situations in which intentional behavior backfires (e.g., triggering failure detection in many SHEM instances)

[This section needs to be expanded: systematically list attack vectors and analyze them in more depth, and also identify safety risks.]

### Failure and Manipulation Detection
- **Internal failures**: The system continously checks for signs of internal failure. If failures within the home energy system are detected, failsafe behavior for the affected parts is triggered (see below). Possible failures include
  - **failure of controllable device**: loss of communication with a device, device reporting an error, erroneous behavior of device (e.g., failure to react to power setpoints)
  - **missing/unusable measurement data**: no data received from a measurement device for a certain time, measured data outside realistic ranges, incompatibilities between different measurements
  - **internal failure**: module reports an error, disk space or memory exhausted, process killed, module does not deliver required data in time (such as optimization result or measurement data)
- **Critical grid states**: Frequency (absolute value and rate of change) and voltage could be monitored to detect critical grid states and act accordingly (see [Real-time control](#real-time-control)).
- **Data verification**: External data such as energy prices or dynamic grid tariffs are used for optimization. This data should be verified, for example by comparing to independent sources for day-ahead auction results and/or by using TLS and certificate pinning.

### Safety Fallback Procedures
When a failure is detected, the home energy management system should try to reach a safe state, either for the whole system or for the parts affected by the failure. However, this fallback should not happen abruptly, as otherwise it might be misused to enact a sudden, large change in load on the power grid.

Safety fallback is implemented on the device level. Every device module includes both failure detection and a method to fall back into a safe state. For a battery storage, this could be a linear, slow reduction of the current power setpoint to zero over several minutes. For an EV charger it might be to slowly reduce the maximum allowed current to its smallest non-zero value.

For all devices, transition to the safe state needs to be slow (taking at least 5 minutes), steps over discontinuous power levels must be spread randomly, and the safe state must be one in which the impact on the power grid is minimal while effects on the user are kept small.

### Operating Environment
All security considerations assume this environment:
- SHEM is running on a dedicated computer (a Raspberry Pi will suffice); disk encryption is encouraged, but in order to restart quickly after a power failure the key has to be stored either on the same disk (this will still make deleting all data easier and more reliable) or on another device connected to the computer (e.g., a Trusted Platform Module (TPM)/Hardware Security Module (HSM) or just a USB drive)
- physical access to this computer is limited to legitimate users
- if a network is used to connect controllable devices and measurement devices, this network is local and protected against external access
- internet access is protected by a firewall that denies connection attempts from the internet

If possible, local devices should be directly connected to the hardware SHEM is running on. In this way a faulty or corrupted device cannot influence the others.

SHEM will trust even local devices as little as possible.

## Architecture
### Module isolation
The functionality is broken down into modules that are kept isolated from each other. They run in their own container or even their own virtual machine, can only communicate with the orchestrator (see below), can interact, if applicable, with only a single controllable device or measurement device, have no or very limited possibilities to save their state, and their usage of shared resources (disk, CPU, memory) is limited.

Modules include
- a controller for a specific controllable device (e.g. a module that talks to the EV charger)
- a driver for a specific measurement device (e.g. a module that receives values from the energy meter)
- a communicator that accesses the internet (e.g., for fetching energy prices or contacting the user)
- the optimizer that plans dispatch of controllable devices
- the real-time controller that executes the optimized plan; it could also detect critical states of the power grid and refrain from actions that might further endanger it

### Orchestrator
All modules are started, controlled, and stopped by the orchestrator. The orchestrator is the only part of SHEM that runs directly on hardware instead of being containerized.

Because of this central role, the orchestrator should depend on as little dependencies as possible in order to keep the trusted computing base small.

Functionality of the orchestrator include
- downloading, verifying, updating, starting and stopping modules
- assigning resources to modules (disk space, memory, CPU time, connection to external devices)
- providing configuration data and a communication channel to modules
- sanitizing and rate-limiting data from modules and passing it on
- detecting module failure and trying to resolve it by restarting
- storing historical data [could be outsourced to a module]
- allow the user to change configuration in a safe way (validate configuration data, require authentication and (local) attestation)
- logging and communicating important errors to the user immediately

## Implementation
### Module containers
Every instance of every module lives in its own container. The isolation between containers makes it harder to exploit security defects in a module and to contain the effects of a successful attack on a module.

SHEM will make use of OCI containers (docker, podman). The isolation might be improved giving each module instance its own virtual machine (like in Kata Containers or Qubes OS), or by using FreeBSD jails.

### Communication between modules
Every module can only communicate to the orchestrator. If it requires data from other modules, it can request it from the orchestrator.

All communication uses a simple data format that is validated by the orchestrator before any data is passed on. The orchestrator also verifies that a module can only send data it is supposed to send and rate-limits the amount of data sent.

### Communication with devices
The connection to a local device is made via USB or network. The corresponding logical device (USB tty device or network socket) is assigned exclusively to the module that manages the local device.

### Communication with the outside world
Internet access is only provided to modules that need it. Any other module will have no connection to the internet. If possible, their time will be warped to make coordinated attacks more difficult to synchronize.

### Data format
In order to assure an unambiguous interpretation of data and to make it more difficult to smuggle malformed data through validation, all data is in ASCII format and limited to printable characters. Newlines are indicated by the line feed symbol.

A module can send these types of data:
- **point values**: a name and a single decimal value representing, e.g., a measurement that has just been received
- **time series data**: a name, the timestamp of the first value in ISO format and in UTC, the time step between values in seconds, and a number of decimal values; this can be both historical data or forecasts

Decimal values are printed in the format [+/-]12.345, i.e., as a floating-point value, but without allowing a power of ten. Leading zeros before the decimal point can be omitted, as well as the decimal point if it is not required. A missing value is represented as the string "missing".

### Data storage
Modules can persist data only if they are explicitly allowed to do so. In this case, a directory exclusive for this module is created. All other changes to the filesystem are reset when the module is restarted.

Historical data as stored by the orchestrator is aggregated to 5-minute intervals, as this is sufficient for optimization and forecasts and contains less sensitive information about energy use.

### Limiting resource consumption of modules
The orchestrator ensures that no module can exhaust resources in such a way that other modules are affected. This applies to CPU usage, disk space, and memory.

### Replaying and mocking for what-if scenarios and for testing
The orchestrator can replay historical data to the modules and also replace modules that communicate with local devices or the internet with other modules that mock their behavior.

### Updates
The orchestrator automatically checks for module updates and verifies their signature. It defers updates for a random time (zero to several days) so that bugs do not affect all SHEM instances at the same time. Even critical updates are not rolled out immediately; at the utmost, a critical update can trigger the transition to a safe state and the deactivation of the affected module.

The orchestrator also provides a rollback mechanism that allows to go back to a previous module version.

### Backup and restore
The orchestrator allows to make manual and automatic backups. These backups contain only
- the configuration files of orchestrator and modules
- historical data in easily parseable and verifiable data files
- the data directories of modules that require storage

It should be possible to replace a running SHEM instance with a new one in very short time by just copying some small configuration files and, optionally, subsequently importing historical data (that is validated before importing it) and, if necessary, module storage directories.

### Logging and error notification
The orchestrator creates logs via the mechanism provided by the system it is running on. If a non-recoverable error occurs, it will try to contact the user in a way that can be configured beforehand.

### Development
The development should ideally make use of these security mechanisms:
- reproducible builds
- a secure build system
- signed releases
- two-person integrity
- depend only on well-maintained software with a good security record and keep dependencies minimal
