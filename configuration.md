# Configuration

## Module Configuration
The configuration of each module is stored in the directory $SHEM_HOME/modules/[module_name]. It can contain several files and folders, of which all but the `image` file are optional. For example, the configuration directory for the orchestrator (which uses the reserved module name "orchestrator") could look like this (file contents are shown in square brackets, with `\n` for newline characters):

```
$SHEM_HOME/modules/orchestrator/
|-- image  [quay.io/shem/shem-orchestrator]
|-- public_key  [cQyjQftwIlSGYvWjfDMzpr0B5/Lr/S8jDFfVW3hOBk0=]
|-- current_version  [0.0.4]
|-- blacklist  [0.0.2\n0.0.3]
|-- module.conf
|-- storage/
```

The name of the directory is the name of the module. The files and the directory have these meanings:
- `image`: the container image (without version-architecture tag) that this module uses; several modules can use the same image, or even different versions of the same image
- `public_key`: if this is supplied, automatic updates are enabled and checked against this key (see [./update-mechanism.md](update-mechanism.md) for details)
- `current_version`: the version the orchestrator will use (except during updates); if left out, the orchestrator selects the newest locally available version and saves the version number into this file
- `blacklist`: contains blacklisted version numbers, one per line
- `module.conf`: a configuration file that is copied into the module's container
- `storage/`: modules that are allowed to persist data will have this directory mounted into the container