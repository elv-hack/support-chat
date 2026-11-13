#!/usr/bin/env python3
import sys
import release
import ops
if __name__=='__main__':
    try:
        if len(sys.argv)<2:raise ValueError('Commands: check, build, bundle, init, deploy, backup, rollback, recover, restore, status, logs')
        (release.main if sys.argv[1] in {'check','build','bundle'} else ops.main)(sys.argv[1:])
    except (ValueError,RuntimeError,OSError) as error:
        print(f'ERROR: {error}',file=sys.stderr);sys.exit(1)
