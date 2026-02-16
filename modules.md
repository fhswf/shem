
## Module Configuration
The configuration of each module is stored in the directory $SHEM_HOME/modules/[module_name]. It can contain several files and folders, of which all but the `image` file are optional. For example, the configuration directory for the orchestrator (which uses the reserved module name "orchestrator") could look like this (file contents are shown in square brackets, with `\n` for newline characters):

```
$SHEM_HOME/modules/orchestrator/
|-- image  [quay.io/shem/shem-orchestrator]
|-- public_key  [cQyjQftwIlSGYvWjfDMzpr0B5/Lr/S8jDFfVW3hOBk0=]
|-- current_version  [0.0.4]
|-- blacklist  [0.0.2\n0.0.3]
|-- inputs  []
|-- module-config/
|-- storage/
```

The name of the directory is the name of the module. It must consist only of alphanumerical characters and the underscore character (a-z, A-Z, 0-9, _). Its maximum length is 100 characters.

The files and the directory have these meanings:
- `image`: the container image (without version-architecture tag) that this module uses; several modules can use the same image, or even different versions of the same image
- `public_key`: if this is supplied, automatic updates are enabled and checked against this key (see [./update-mechanism.md](update-mechanism.md) for details)
- `current_version`: the version the orchestrator will use (except during updates); if left out, the orchestrator selects the newest locally available version and saves the version number into this file
- `blacklist`: contains blacklisted version numbers, one per line
- `inputs`: specifies which messages from other modules this module receives (see [Message Routing](#message-routing))
- `module-config/`: a directory for configuration files that is mounted read-only into the module's container
- `storage/`: modules that are allowed to persist data will have this directory mounted into the container

### Orchestrator additional options
These options can be set by creating a file named after the option in `$SHEM_HOME/modules/orchestrator/`:
- `UpdateCheckIntervalHours`: Update check interval in hours (default: 22.15)
- `UpdateDelayMaxHours`: Maximum update delay in hours for staggered updates across instances (default: 96.0)

## Module Communication
Each module communicates with the orchestrator via its standard input (stdin), standard output (stdout) and standard error (stderr). Notifications including error messages are sent via stderr, messages containing values in a certain format are sent via stdout.

All messages are ASCII encoded and limited to the printable character set (0x20 to 0x7E) plus the newline character (0x0A). Numerical values are represented as text. These rules ensure that the messages are human-readable and that there are no ambiguities in how to parse the message. The orchestrator enforces message validity.

The Go library `shemmsg` (in the `shemmsg/` directory) provides parsing, validation, and encoding of messages. It is the same code as used by the orchestrator and has been outsourced so that it can be used by other modules as well.

### Notifications and Error Messages
These messages are sent via stderr. Each line (i.e., a string of ASCII characters ending with a newline symbol) is treated as a single message. Message length is limited to 1000 characters (not counting the newline symbol). It may start with a string like "<3>" or "<7>" to indicate the log level (see [man 3 sd-daemon](https://manpages.debian.org/trixie/libsystemd-dev/sd-daemon.3.en.html)).

Messages of this type are not parsed by the orchestrator. Depending on logging config, they may be logged.

### Parsed Messages
Parsed messages are sent and received via stdout and stdin. They have a type, which determines their format, and also a name of the variable whose value they contain. The first line consists of type and name, divided by a space character. Messages are divided by two or more consecutive newline characters (i.e., an empty line). You can therefore start and end each message sent with two newline characters. Here is an example of a valid message of type `pointvalue` that gives the current value of the variable `net_power` of the sending module:

```


pointvalue net_power
-802.10


```

Message length is limited to 10000 bytes (not counting newlines). The variable name consists of alphanumerical characters and the underscore character (a-z, A-Z, 0-9 and _). It must be at most 100 characters long. The orchestrator will expand the name to a fully qualified name in the form `module_name.variable_name`, where `module_name` is the name of the originating module. When a module sends a message, it leaves out its module name.

The message types and their descriptions follow.

#### Point Values
In messages of type `pointvalue`, the type/name line is followed by a single line containing a decimal number in the format `(-)12345678.123`. Numbers must therefore have an absolute value smaller than 100 million. Leading zeros and trailing zeros after the decimal point can be omitted, as well as the decimal point if only zeros would follow. The value may be missing, in which case it is represented by the string "missing".

Values for electric power should be given in kilowatts, and electric energy in kilowatt-hours.

Examples:
```
pointvalue net_power
-802.10


pointvalue total_energy
9371802

pointvalue irradiance
missing
```

The value always refers to the current point in time. Otherwise, a time series (see below) with a single value may be used.

#### Time Series
In messages of type timeseries, the type/name line is followed by a line containing the UTC timestamp of the first value in the format `yyyy-mm-ddThh:mm`. Each subsequent line contains a single value, following the same rules as for point values.

**All time series use a fixed time step of 5 minutes.** The minutes component of the timestamp must be a multiple of 5 (i.e., 00, 05, 10, …, 55). If a quantity changes more slowly (e.g., hourly energy prices), the value is simply repeated for each 5-minute interval. This fixed time step simplifies parsing and eliminates errors from inconsistent intervals.

For instantaneous measurements (e.g., meter readings) reported as a time series, the timestamp indicates when the measurement was taken. For values that represent a quantity over a duration (such as energy prices or power averages), **time series are left-labeled**. This means each value applies to the 5-minute interval beginning at its timestamp. For example, a value with timestamp 2025-12-06T08:00 applies to the interval from 08:00:00 up to but not including 08:05:00 UTC.

Example:
```

timeseries pv_forecast
2025-12-06T08:00
120.0
145.1
missing
140.5

```

### Module Shutdown
The orchestrator closes stdin when it wants to shut down a module. Modules should therefore monitor the closing of stdin. If a module does not exit within a certain time after stdin is closed, the orchestrator will forcibly shut it down.

### Module Malfunction Detection
The orchestrator will interpret a too large rate of messages or many malformed messages as a sign of module failure.


## Message Routing
Modules cannot communicate directly with each other. All message routing is controlled by the orchestrator based on configuration files. This ensures that data flow between modules is determined by the user, not by the modules themselves.

### Message Processing
When a module sends a parsed message (via stdout), the orchestrator:

1. validates the format and parses the message,
2. qualifies the variable name by prefixing it with the module name (e.g., `net_power` from the `meter` module becomes `meter.net_power`), and
3. reconstructs and sends the message to all subscribing modules.

The orchestrator's parsing and re-emission ensures that all forwarded messages are in a consistent format and protects against malformed data.

### The `inputs` File
A module's subscriptions are configured in the `inputs` file within its configuration directory. Each line specifies a pattern for messages the module wishes to receive. Empty lines and lines containing only whitespace are ignored.

The file supports two forms:

```
module_name.variable_name
module_name.variable_name localname
```

The first form subscribes to messages from module `module_name` for the value `value_name`. Both `module_name` and `value_name` may be the wildcard "`*`" to indicate that messages from all modules or for all values are to be forwarded. The pattern "`*.*`" is also valid (e.g., for data logging).

The second form supplies an alias `localname` for the variable. These messages are delivered with the name `localname` instead of `module_name.variable_name`. Wildcards are not allowed in this case.

If no messages are to be received, the input file can be either empty or missing. If several lines in a single `inputs` file match the same message, the module receives the message several times.

Example `inputs` file:

```
meter.net_power
optimizer.device_2_setpoint setpoint
*.temperature
gui.*
```

This module would receive:
- `meter.net_power` (as `meter.net_power`)
- `optimizer.device_2_setpoint` as `setpoint`
- `temperature` values from all modules (under their fully qualified names)
- all values from module `gui` (under their fully qualified names)


### Validation
The orchestrator validates all `inputs` files:

- **Invalid patterns** generate an error message (but the module will still keep running)
- **Unknown module names** in non-wildcard patterns generate a warning message 
