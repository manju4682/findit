export namespace device {
	
	export class Device {
	    id: string;
	    node: string;
	    rawNode: string;
	    name: string;
	    size: number;
	    blockSize: number;
	    removable: boolean;
	    protocol: string;
	    internal: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Device(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.node = source["node"];
	        this.rawNode = source["rawNode"];
	        this.name = source["name"];
	        this.size = source["size"];
	        this.blockSize = source["blockSize"];
	        this.removable = source["removable"];
	        this.protocol = source["protocol"];
	        this.internal = source["internal"];
	    }
	}

}

export namespace main {
	
	export class PartitionDTO {
	    offset: number;
	    fsType: string;
	    label: string;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new PartitionDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.offset = source["offset"];
	        this.fsType = source["fsType"];
	        this.label = source["label"];
	        this.size = source["size"];
	    }
	}
	export class PreviewDTO {
	    kind: string;
	    status: string;
	    note: string;
	    width: number;
	    height: number;
	    thumbnailDataUrl: string;
	
	    static createFrom(source: any = {}) {
	        return new PreviewDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.status = source["status"];
	        this.note = source["note"];
	        this.width = source["width"];
	        this.height = source["height"];
	        this.thumbnailDataUrl = source["thumbnailDataUrl"];
	    }
	}
	export class RecoverItemDTO {
	    name: string;
	    path: string;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new RecoverItemDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.error = source["error"];
	    }
	}
	export class RecoverDTO {
	    written: number;
	    failed: number;
	    items: RecoverItemDTO[];
	
	    static createFrom(source: any = {}) {
	        return new RecoverDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.written = source["written"];
	        this.failed = source["failed"];
	        this.items = this.convertValues(source["items"], RecoverItemDTO);
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

