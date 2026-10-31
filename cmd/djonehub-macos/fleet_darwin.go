//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOKitLib.h>
#include <stdlib.h>
#include <stdio.h>
#include <string.h>
static uint64_t fleetNumber(io_registry_entry_t e, CFStringRef key) {
 CFTypeRef v=IORegistryEntryCreateCFProperty(e,key,kCFAllocatorDefault,0);
 int64_t n=0;
 if(v){if(CFGetTypeID(v)==CFNumberGetTypeID()) CFNumberGetValue(v,kCFNumberSInt64Type,&n);CFRelease(v);}
 return (uint64_t)n;
}
static int fleetSupported(io_registry_entry_t e) {
 uint64_t v=fleetNumber(e,CFSTR("idVendor")),p=fleetNumber(e,CFSTR("idProduct"));
 return (v==0x2ca3&&p==0x4006)||(v==0x2c7c&&p==0x0125);
}
static uint64_t fleetInterfaceSession(const char *name) {
 io_registry_entry_t e=IOServiceGetMatchingService(kIOMainPortDefault,IOBSDNameMatching(kIOMainPortDefault,0,name));
 while(e){
  if(IOObjectConformsTo(e,"IOUSBHostDevice")){
   uint64_t s=fleetSupported(e)?fleetNumber(e,CFSTR("sessionID")):0;
   IOObjectRelease(e);return s;
  }
  io_registry_entry_t parent=0;
  kern_return_t result=IORegistryEntryGetParentEntry(e,kIOServicePlane,&parent);
  IOObjectRelease(e);e=(result==KERN_SUCCESS)?parent:0;
 }
 return 0;
}
static void fleetSetNumber(CFMutableDictionaryRef d,CFStringRef key,uint64_t n){
 int64_t value=(int64_t)n;CFNumberRef v=CFNumberCreate(NULL,kCFNumberSInt64Type,&value);
 CFDictionarySetValue(d,key,v);CFRelease(v);
}
static char *fleetInventoryXML(int *length){
 io_iterator_t iterator=0;
 if(IOServiceGetMatchingServices(kIOMainPortDefault,IOServiceMatching("IOUSBHostDevice"),&iterator)!=KERN_SUCCESS)return NULL;
 CFMutableArrayRef array=CFArrayCreateMutable(NULL,0,&kCFTypeArrayCallBacks);
 io_service_t e;
 while((e=IOIteratorNext(iterator))){
  if(fleetSupported(e)){
   CFMutableDictionaryRef d=CFDictionaryCreateMutable(NULL,0,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
   fleetSetNumber(d,CFSTR("location"),fleetNumber(e,CFSTR("locationID")));
   fleetSetNumber(d,CFSTR("vendor"),fleetNumber(e,CFSTR("idVendor")));
   fleetSetNumber(d,CFSTR("product_id"),fleetNumber(e,CFSTR("idProduct")));
   char session[32];snprintf(session,sizeof(session),"%llu",(unsigned long long)fleetNumber(e,CFSTR("sessionID")));
   CFStringRef s=CFStringCreateWithCString(NULL,session,kCFStringEncodingUTF8);CFDictionarySetValue(d,CFSTR("session"),s);CFRelease(s);
   CFTypeRef serial=IORegistryEntryCreateCFProperty(e,CFSTR("USB Serial Number"),NULL,0);
   if(serial){if(CFGetTypeID(serial)==CFStringGetTypeID())CFDictionarySetValue(d,CFSTR("serial"),serial);CFRelease(serial);}
   CFTypeRef product=IORegistryEntryCreateCFProperty(e,CFSTR("USB Product Name"),NULL,0);
   if(product){if(CFGetTypeID(product)==CFStringGetTypeID())CFDictionarySetValue(d,CFSTR("product"),product);CFRelease(product);}
   CFArrayAppendValue(array,d);CFRelease(d);
  }
  IOObjectRelease(e);
 }
 IOObjectRelease(iterator);
 CFDataRef data=CFPropertyListCreateData(NULL,array,kCFPropertyListXMLFormat_v1_0,0,NULL);CFRelease(array);
 if(!data)return NULL;
 *length=(int)CFDataGetLength(data);char *out=malloc(*length);
 if(out)memcpy(out,CFDataGetBytePtr(data),*length);CFRelease(data);return out;
}
*/
import "C"

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

func discoverFleetDevices() ([]fleetDevice, error) {
	var length C.int
	raw := C.fleetInventoryXML(&length)
	if raw == nil {
		return nil, errors.New("USB inventory unavailable")
	}
	defer C.free(unsafe.Pointer(raw))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", "-")
	command.Stdin = bytes.NewReader(C.GoBytes(unsafe.Pointer(raw), length))
	b, err := command.Output()
	if err != nil {
		return nil, err
	}
	var devices []fleetDevice
	if err = json.Unmarshal(b, &devices); err != nil {
		return nil, err
	}
	for _, iface := range discoverMacNetworkInterfaces() {
		session := fleetSessionForInterface(iface.Name)
		for i := range devices {
			if session != 0 && devices[i].Session == strconv.FormatUint(session, 10) {
				devices[i].Interfaces = append(devices[i].Interfaces, iface.Name)
			}
		}
	}
	return devices, nil
}
func fleetSessionForInterface(name string) uint64 {
	c := C.CString(name)
	defer C.free(unsafe.Pointer(c))
	return uint64(C.fleetInterfaceSession(c))
}

func lockFleetState(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("another module supervisor owns this state directory")
	}
	return func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); file.Close() }, nil
}
