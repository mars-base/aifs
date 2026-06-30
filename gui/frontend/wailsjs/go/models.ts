export namespace main {
	
	export class BenchResult {
	    writeBigMiBs: number;
	    writeBigSecsPerFile: number;
	    readBigMiBs: number;
	    readBigSecsPerFile: number;
	    writeSmallPerSec: number;
	    writeSmallMsPerFile: number;
	    readSmallPerSec: number;
	    readSmallMsPerFile: number;
	    statPerSec: number;
	    statMsPerFile: number;
	
	    static createFrom(source: any = {}) {
	        return new BenchResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.writeBigMiBs = source["writeBigMiBs"];
	        this.writeBigSecsPerFile = source["writeBigSecsPerFile"];
	        this.readBigMiBs = source["readBigMiBs"];
	        this.readBigSecsPerFile = source["readBigSecsPerFile"];
	        this.writeSmallPerSec = source["writeSmallPerSec"];
	        this.writeSmallMsPerFile = source["writeSmallMsPerFile"];
	        this.readSmallPerSec = source["readSmallPerSec"];
	        this.readSmallMsPerFile = source["readSmallMsPerFile"];
	        this.statPerSec = source["statPerSec"];
	        this.statMsPerFile = source["statMsPerFile"];
	    }
	}
	export class ConfigStatus {
	    exists: boolean;
	    path: string;
	    baseDir: string;
	
	    static createFrom(source: any = {}) {
	        return new ConfigStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.exists = source["exists"];
	        this.path = source["path"];
	        this.baseDir = source["baseDir"];
	    }
	}
	export class CreateInstanceRequest {
	    name: string;
	    data_dir: string;
	    pitr_enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CreateInstanceRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.data_dir = source["data_dir"];
	        this.pitr_enabled = source["pitr_enabled"];
	    }
	}
	export class InstanceInfo {
	    name: string;
	    status: string;
	    running: boolean;
	    pgUrl: string;
	    mountPath: string;
	    isFormatted: boolean;
	
	    static createFrom(source: any = {}) {
	        return new InstanceInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.status = source["status"];
	        this.running = source["running"];
	        this.pgUrl = source["pgUrl"];
	        this.mountPath = source["mountPath"];
	        this.isFormatted = source["isFormatted"];
	    }
	}

}

export namespace pitr {
	
	export class Snapshot {
	    name: string;
	    // Go type: time
	    timestamp: any;
	    // Go type: time
	    stop_time: any;
	    type: string;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.timestamp = this.convertValues(source["timestamp"], null);
	        this.stop_time = this.convertValues(source["stop_time"], null);
	        this.type = source["type"];
	        this.size = source["size"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

