package com.supportchat.app;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.net.Uri;
import android.os.Bundle;
import android.view.View;
import android.view.WindowManager;
import android.webkit.*;
import android.widget.*;

public class MainActivity extends Activity {
    private WebView web;
    private String server;
    private ValueCallback<Uri[]> chooser;
    private PermissionRequest microphone;
    private boolean externalFlow=false, resetOnResume=false;
    private static final int FILE_PICKER=21, AUDIO_PERMISSION=22;

    @Override public void onCreate(Bundle state){
        super.onCreate(null); // Never restore a WebView session or form fields.
        getWindow().addFlags(WindowManager.LayoutParams.FLAG_SECURE);
        getWindow().setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
        server=BuildConfig.SERVER_URL;
        if(server.isEmpty())server=getPreferences(MODE_PRIVATE).getString("server_url","");
        if(server.isEmpty())showSetup();else openChat();
    }
    private boolean validServer(String value){
        Uri u=Uri.parse(value);String scheme=u.getScheme(),host=u.getHost();
        if(host==null||u.getUserInfo()!=null||u.getQuery()!=null||u.getFragment()!=null)return false;
        if(u.getPath()!=null&&!u.getPath().isEmpty()&&!u.getPath().equals("/"))return false;
        if("https".equals(scheme))return true;
        return BuildConfig.DEBUG&&"http".equals(scheme)&&(host.equals("127.0.0.1")||host.equals("localhost")||host.equals("10.0.2.2"));
    }
    private void showSetup(){
        LinearLayout layout=new LinearLayout(this);layout.setOrientation(LinearLayout.VERTICAL);layout.setPadding(56,140,56,56);layout.setBackgroundColor(Color.rgb(248,249,245));
        TextView title=new TextView(this);title.setText("一对一 · 连接服务");title.setTextSize(25);title.setTextColor(Color.rgb(39,91,76));layout.addView(title);
        TextView tip=new TextView(this);tip.setText("请输入管理员提供的服务地址。此设置只需填写一次。");tip.setPadding(0,30,0,25);layout.addView(tip);
        EditText input=new EditText(this);input.setSingleLine(true);input.setHint("https://chat.example.com");input.setInputType(android.text.InputType.TYPE_CLASS_TEXT|android.text.InputType.TYPE_TEXT_VARIATION_URI);input.setImportantForAutofill(View.IMPORTANT_FOR_AUTOFILL_NO);layout.addView(input);
        Button connect=new Button(this);connect.setText("连接");layout.addView(connect);connect.setOnClickListener(v->{String value=input.getText().toString().trim().replaceAll("/+$","");if(!validServer(value)){input.setError("请输入有效 HTTPS 地址；调试版也支持本机地址");return;}server=value;getPreferences(MODE_PRIVATE).edit().putString("server_url",server).apply();openChat();});setContentView(layout);
    }
    private boolean sameOrigin(Uri u){Uri base=Uri.parse(server);return base.getScheme().equals(u.getScheme())&&base.getHost().equals(u.getHost())&&base.getPort()==u.getPort();}
    private void openChat(){
        if(!validServer(server)){server="";showSetup();return;}
        CookieManager.getInstance().setAcceptCookie(false);CookieManager.getInstance().removeAllCookies(null);WebStorage.getInstance().deleteAllData();
        web=new WebView(this);web.setSaveEnabled(false);web.setImportantForAutofill(View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS);
        web.setOnApplyWindowInsetsListener((v,insets)->{v.setPadding(insets.getSystemWindowInsetLeft(),insets.getSystemWindowInsetTop(),insets.getSystemWindowInsetRight(),insets.getSystemWindowInsetBottom());return insets.consumeSystemWindowInsets();});
        WebSettings settings=web.getSettings();settings.setJavaScriptEnabled(true);settings.setDomStorageEnabled(false);settings.setDatabaseEnabled(false);settings.setCacheMode(WebSettings.LOAD_NO_CACHE);settings.setAllowFileAccess(false);settings.setAllowContentAccess(true);settings.setSaveFormData(false);settings.setGeolocationEnabled(false);settings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);settings.setMediaPlaybackRequiresUserGesture(true);
        WebView.setWebContentsDebuggingEnabled(false);
        web.clearCache(true);web.clearFormData();web.clearHistory();
        web.setWebViewClient(new WebViewClient(){
            @Override public boolean shouldOverrideUrlLoading(WebView view,WebResourceRequest request){return !sameOrigin(request.getUrl());}
            @Override public void onReceivedError(WebView view,WebResourceRequest request,WebResourceError error){if(request.isForMainFrame())runOnUiThread(()->showError());}
        });
        web.setDownloadListener((url,agent,disposition,type,length)->Toast.makeText(this,"此应用不支持保存附件到本机",Toast.LENGTH_SHORT).show());
        web.setWebChromeClient(new WebChromeClient(){
            @Override public boolean onShowFileChooser(WebView view,ValueCallback<Uri[]> callback,FileChooserParams params){
                if(chooser!=null)chooser.onReceiveValue(null);chooser=callback;externalFlow=true;
                Intent intent=new Intent(Intent.ACTION_GET_CONTENT);intent.setType("*/*");intent.addCategory(Intent.CATEGORY_OPENABLE);
                try{startActivityForResult(Intent.createChooser(intent,"选择图片或文件"),FILE_PICKER);}catch(Exception e){chooser.onReceiveValue(null);chooser=null;externalFlow=false;}return true;
            }
            @Override public void onPermissionRequest(PermissionRequest request){runOnUiThread(()->{
                if(!sameOrigin(request.getOrigin())){request.deny();return;}
                boolean audio=false;for(String resource:request.getResources())if(PermissionRequest.RESOURCE_AUDIO_CAPTURE.equals(resource))audio=true;
                if(!audio){request.deny();return;}
                if(checkSelfPermission(Manifest.permission.RECORD_AUDIO)==PackageManager.PERMISSION_GRANTED){request.grant(new String[]{PermissionRequest.RESOURCE_AUDIO_CAPTURE});return;}
                if(microphone!=null)microphone.deny();microphone=request;externalFlow=true;requestPermissions(new String[]{Manifest.permission.RECORD_AUDIO},AUDIO_PERMISSION);
            });}
            @Override public void onPermissionRequestCanceled(PermissionRequest request){if(microphone==request)microphone=null;}
        });
        setContentView(web);web.loadUrl(server+BuildConfig.ENTRY_PATH);
    }
    private void showError(){new AlertDialog.Builder(this).setTitle("暂时无法连接").setMessage("请检查网络和服务地址后重试。").setPositiveButton("重试",(d,w)->web.loadUrl(server+BuildConfig.ENTRY_PATH)).setNegativeButton("设置地址",(d,w)->{if(web!=null){web.destroy();web=null;}server="";getPreferences(MODE_PRIVATE).edit().remove("server_url").apply();showSetup();}).show();}
    @Override protected void onActivityResult(int req,int result,Intent data){super.onActivityResult(req,result,data);if(req==FILE_PICKER){externalFlow=false;if(chooser!=null){chooser.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(result,data));chooser=null;}}}
    @Override public void onRequestPermissionsResult(int req,String[] permissions,int[] grants){super.onRequestPermissionsResult(req,permissions,grants);if(req==AUDIO_PERMISSION){externalFlow=false;if(microphone!=null){if(grants.length>0&&grants[0]==PackageManager.PERMISSION_GRANTED)microphone.grant(new String[]{PermissionRequest.RESOURCE_AUDIO_CAPTURE});else microphone.deny();microphone=null;}}}
    @Override protected void onPause(){super.onPause();if(web!=null)web.onPause();}
    @Override protected void onStop(){super.onStop();if(web!=null&&!externalFlow){resetOnResume=true;web.loadUrl("about:blank");web.clearCache(true);web.clearHistory();}}
    @Override protected void onResume(){super.onResume();if(web!=null){web.onResume();if(resetOnResume){resetOnResume=false;web.loadUrl(server+BuildConfig.ENTRY_PATH);}}}
    @Override public void onBackPressed(){if(web!=null)new AlertDialog.Builder(this).setMessage("退出并结束本次会话？").setNegativeButton("取消",null).setPositiveButton("退出",(d,w)->finish()).show();else super.onBackPressed();}
    @Override protected void onDestroy(){if(chooser!=null)chooser.onReceiveValue(null);if(microphone!=null)microphone.deny();if(web!=null){web.loadUrl("about:blank");web.clearCache(true);web.clearHistory();web.clearFormData();web.destroy();web=null;}super.onDestroy();}
}
